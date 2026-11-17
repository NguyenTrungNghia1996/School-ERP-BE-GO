package tenant_mgmt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"school-erp/internal/pkg/cloudflare"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/audit"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrOrgNotFound              = errors.New("organization not found")
	ErrHistoryNotFound          = errors.New("subdomain history not found")
	ErrSubdomainInvalidFormat   = errors.New("invalid subdomain format")
	ErrSubdomainReservedOrTaken = errors.New("subdomain is reserved or already taken")
	ErrOrgNotPending            = errors.New("organization is not in PENDING_APPROVAL status")
)

// Subdomain lowercase, a-z, 0-9, -, dài 3-40 ký tự, không bắt đầu/kết thúc bằng dấu gạch ngang
var subdomainRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

type Service struct {
	db          *gorm.DB
	rdb         *redis.Client
	cf          *cloudflare.Client
	auditor     *audit.Service
	provisioner *DNSProvisioner
}

func NewService(db *gorm.DB, rdb *redis.Client, cf *cloudflare.Client, auditor *audit.Service) *Service {
	provisioner := NewDNSProvisioner(db, rdb, cf, auditor)
	return &Service{
		db:          db,
		rdb:         rdb,
		cf:          cf,
		auditor:     auditor,
		provisioner: provisioner,
	}
}

func (s *Service) validateSubdomain(tx *gorm.DB, subdomain string, excludeOrgID *uuid.UUID) error {
	subdomain = strings.ToLower(strings.TrimSpace(subdomain))
	if !subdomainRegex.MatchString(subdomain) {
		return ErrSubdomainInvalidFormat
	}

	var reservedCount int64
	if err := tx.Model(&ReservedSubdomain{}).Where("LOWER(keyword) = ?", subdomain).Count(&reservedCount).Error; err != nil {
		return err
	}
	if reservedCount > 0 {
		return ErrSubdomainReservedOrTaken
	}

	q := tx.Model(&Organization{}).Where("LOWER(subdomain) = ?", subdomain)
	if excludeOrgID != nil {
		q = q.Where("id <> ?", *excludeOrgID)
	}
	var takenCount int64
	if err := q.Count(&takenCount).Error; err != nil {
		return err
	}
	if takenCount > 0 {
		return ErrSubdomainReservedOrTaken
	}

	// Kiểm tra không nằm trong các old_subdomain đang còn hạn redirect_until > NOW()
	var redirectCount int64
	if err := tx.Model(&OrganizationSubdomainHistory{}).
		Where("LOWER(old_subdomain) = ? AND old_record_status = 'REDIRECTING' AND (redirect_until IS NULL OR redirect_until > ?)", subdomain, time.Now()).
		Count(&redirectCount).Error; err != nil {
		return err
	}
	if redirectCount > 0 {
		return ErrSubdomainReservedOrTaken
	}

	return nil
}

func (s *Service) ensureSubdomainNotOnCloudflare(ctx context.Context, subdomain string) error {
	if s.cf == nil || !s.cf.IsConfigured() {
		return nil
	}
	rec, err := s.cf.FindRecordByFQDN(ctx, s.cf.BuildFQDN(subdomain))
	if err == nil && rec != nil {
		return ErrSubdomainReservedOrTaken
	}
	return nil
}

type ListOrgsFilter struct {
	Search    string
	Status    string
	DNSStatus string
	Page      int
	Limit     int
}

func (s *Service) ListOrganizations(ctx context.Context, filter ListOrgsFilter) ([]OrganizationSummary, int64, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 20
	}

	q := s.db.WithContext(ctx).Model(&Organization{})
	if filter.Search != "" {
		like := "%" + strings.TrimSpace(filter.Search) + "%"
		q = q.Where("name ILIKE ? OR subdomain ILIKE ? OR code ILIKE ?", like, like, like)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", strings.ToUpper(filter.Status))
	}
	if filter.DNSStatus != "" {
		q = q.Where("dns_status = ?", strings.ToUpper(filter.DNSStatus))
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var orgs []Organization
	offset := (filter.Page - 1) * filter.Limit
	if err := q.Order("created_at DESC").Offset(offset).Limit(filter.Limit).Find(&orgs).Error; err != nil {
		return nil, 0, err
	}

	summaries := make([]OrganizationSummary, 0, len(orgs))
	for _, org := range orgs {
		var schoolCount int64
		s.db.WithContext(ctx).Model(&School{}).Where("organization_id = ?", org.ID).Count(&schoolCount)

		var subInfo struct {
			PackageName *string    `gorm:"column:package_name"`
			ExpiresAt   *time.Time `gorm:"column:expires_at"`
		}
		s.db.WithContext(ctx).Raw(`
			SELECT sp.name AS package_name, os.expires_at
			FROM organization_subscriptions os
			JOIN service_package_versions spv ON spv.id = os.package_version_id
			JOIN service_packages sp ON sp.id = spv.package_id
			WHERE os.organization_id = ?
			  AND os.subscription_type = 'PRIMARY'
			  AND os.status = 'ACTIVE'
			ORDER BY os.expires_at DESC
			LIMIT 1
		`, org.ID).Scan(&subInfo)

		summaries = append(summaries, OrganizationSummary{
			Organization:       org,
			SchoolCount:        schoolCount,
			PrimaryPackageName: subInfo.PackageName,
			PrimaryExpiresAt:   subInfo.ExpiresAt,
		})
	}

	return summaries, total, nil
}

func (s *Service) GetOrganizationDetail(ctx context.Context, orgID uuid.UUID) (map[string]any, error) {
	var org Organization
	if err := s.db.WithContext(ctx).First(&org, "id = ?", orgID).Error; err != nil {
		return nil, ErrOrgNotFound
	}

	var schools []School
	s.db.WithContext(ctx).Where("organization_id = ?", orgID).Order("is_main DESC, created_at ASC").Find(&schools)

	var subscriptions []map[string]any
	s.db.WithContext(ctx).Raw(`
		SELECT os.id, spv.package_id, sp.code AS package_code, sp.name AS package_name,
		       os.package_version_id, spv.version_number AS version_number, spv.version_label AS version_label,
		       os.subscription_type, os.status, os.started_at, os.expires_at,
		       os.custom_quotas, os.auto_renew, os.notes,
		       os.revoked_at, os.revoke_reason, os.created_at
		FROM organization_subscriptions os
		JOIN service_package_versions spv ON spv.id = os.package_version_id
		JOIN service_packages sp ON sp.id = spv.package_id
		WHERE os.organization_id = ?
		ORDER BY os.created_at DESC
	`, orgID).Scan(&subscriptions)
	if subscriptions == nil {
		subscriptions = []map[string]any{}
	}

	var histories []OrganizationSubdomainHistory
	s.db.WithContext(ctx).Where("organization_id = ?", orgID).Order("created_at DESC").Find(&histories)

	return map[string]any{
		"organization":        org,
		"schools":             schools,
		"subscriptions":       subscriptions,
		"subdomain_histories": histories,
	}, nil
}

type CreateOrganizationInput struct {
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	Subdomain    string  `json:"subdomain"`
	ContactEmail *string `json:"contact_email"`
	ContactPhone *string `json:"contact_phone"`
	TaxCode      *string `json:"tax_code"`
	Address      *string `json:"address"`
	School       struct {
		Code    string  `json:"code"`
		Name    string  `json:"name"`
		Level   *string `json:"level"`
		Address *string `json:"address"`
		Phone   *string `json:"phone"`
		Email   *string `json:"email"`
	} `json:"school"`
	Admin struct {
		Email    string  `json:"email"`
		Password string  `json:"password"`
		FullName string  `json:"full_name"`
		Phone    *string `json:"phone"`
	} `json:"admin"`
	ActorID   uuid.UUID `json:"-"`
	IPAddress string    `json:"-"`
	UserAgent string    `json:"-"`
}

func (s *Service) CreateOrganization(ctx context.Context, input CreateOrganizationInput) (map[string]any, error) {
	subdomain := strings.ToLower(strings.TrimSpace(input.Subdomain))
	if !subdomainRegex.MatchString(subdomain) {
		return nil, ErrSubdomainInvalidFormat
	}
	if err := s.ensureSubdomainNotOnCloudflare(ctx, subdomain); err != nil {
		return nil, err
	}
	orgCode := strings.ToUpper(strings.TrimSpace(input.Code))
	if orgCode == "" {
		orgCode = fmt.Sprintf("ORG-%s", strings.ToUpper(subdomain))
	}

	schoolCode := strings.ToUpper(strings.TrimSpace(input.School.Code))
	if schoolCode == "" {
		schoolCode = "MAIN"
	}
	schoolName := strings.TrimSpace(input.School.Name)
	if schoolName == "" {
		schoolName = strings.TrimSpace(input.Name)
	}

	hashedPass, err := bcrypt.GenerateFromPassword([]byte(input.Admin.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	var org Organization
	var school School
	var adminUser TenantUser

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.validateSubdomain(tx, subdomain, nil); err != nil {
			return err
		}

		now := time.Now()
		org = Organization{
			ID:           uuid.New(),
			Code:         orgCode,
			Name:         strings.TrimSpace(input.Name),
			Subdomain:    subdomain,
			ContactEmail: input.ContactEmail,
			ContactPhone: input.ContactPhone,
			TaxCode:      input.TaxCode,
			Address:      input.Address,
			Status:       "ACTIVE",
			DNSStatus:    "PROVISIONING",
			ApprovedBy:   &input.ActorID,
			ApprovedAt:   &now,
		}
		if err := tx.Create(&org).Error; err != nil {
			return err
		}

		school = School{
			ID:             uuid.New(),
			OrganizationID: org.ID,
			Code:           schoolCode,
			Name:           schoolName,
			Level:          input.School.Level,
			Address:        input.School.Address,
			Phone:          input.School.Phone,
			Email:          input.School.Email,
			IsMain:         true,
			Status:         "ACTIVE",
		}
		if err := tx.Create(&school).Error; err != nil {
			return err
		}

		adminEmail := strings.ToLower(strings.TrimSpace(input.Admin.Email))
		err := tx.Where("LOWER(email) = ?", adminEmail).First(&adminUser).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			adminUser = TenantUser{
				ID:                 uuid.New(),
				Email:              adminEmail,
				PasswordHash:       string(hashedPass),
				FullName:           strings.TrimSpace(input.Admin.FullName),
				Phone:              input.Admin.Phone,
				Status:             "ACTIVE",
				MustChangePassword: true,
			}
			if err := tx.Create(&adminUser).Error; err != nil {
				return err
			}
		}

		membership := SchoolMembership{
			ID:             uuid.New(),
			OrganizationID: org.ID,
			SchoolID:       school.ID,
			UserID:         adminUser.ID,
			IsSchoolAdmin:  true,
			Status:         "ACTIVE",
		}
		if err := tx.Create(&membership).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &input.ActorID,
			OrganizationID: &org.ID,
			SchoolID:       &school.ID,
			Action:         "PLATFORM_ORG_CREATED",
			ResourceType:   "ORGANIZATION",
			ResourceID:     &org.ID,
			AfterData: map[string]any{
				"organization": org,
				"school":       school,
				"school_admin": map[string]any{
					"id":        adminUser.ID,
					"email":     adminUser.Email,
					"full_name": adminUser.FullName,
				},
			},
			IPAddress: input.IPAddress,
			UserAgent: input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.TenantSubdomainKey(subdomain)).Err()
	}

	// Đẩy job cấp phát CNAME lên Cloudflare bất đồng bộ
	s.provisioner.EnqueueProvision(org.ID)

	return map[string]any{
		"organization": org,
		"school":       school,
		"school_admin": adminUser,
	}, nil
}

func (s *Service) ApproveOrganization(ctx context.Context, orgID, actorID uuid.UUID, ip, ua string) (*Organization, error) {
	var org Organization
	if err := s.db.WithContext(ctx).First(&org, "id = ?", orgID).Error; err != nil {
		return nil, ErrOrgNotFound
	}
	if org.Status != "PENDING_APPROVAL" {
		return nil, ErrOrgNotPending
	}

	oldStatus := org.Status
	now := time.Now()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&org).Updates(map[string]any{
			"status":         "ACTIVE",
			"dns_status":     "PROVISIONING",
			"dns_last_error": nil,
			"approved_by":    actorID,
			"approved_at":    now,
			"updated_at":     now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &actorID,
			OrganizationID: &org.ID,
			Action:         "PLATFORM_ORG_APPROVED",
			ResourceType:   "ORGANIZATION",
			ResourceID:     &org.ID,
			BeforeData:     map[string]any{"status": oldStatus},
			AfterData:      map[string]any{"status": "ACTIVE", "dns_status": "PROVISIONING", "approved_by": actorID},
			IPAddress:      ip,
			UserAgent:      ua,
		})
	})
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.TenantSubdomainKey(org.Subdomain)).Err()
	}

	s.provisioner.EnqueueProvision(org.ID)

	org.Status = "ACTIVE"
	org.DNSStatus = "PROVISIONING"
	org.ApprovedBy = &actorID
	org.ApprovedAt = &now
	return &org, nil
}

func (s *Service) UpdateOrganizationStatus(ctx context.Context, orgID uuid.UUID, newStatus, reason string, actorID uuid.UUID, ip, ua string) (*Organization, error) {
	newStatus = strings.ToUpper(strings.TrimSpace(newStatus))
	var org Organization
	if err := s.db.WithContext(ctx).First(&org, "id = ?", orgID).Error; err != nil {
		return nil, ErrOrgNotFound
	}

	oldStatus := org.Status
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&org).Updates(map[string]any{
			"status":        newStatus,
			"status_reason": reason,
			"updated_at":    time.Now(),
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &actorID,
			OrganizationID: &org.ID,
			Action:         "PLATFORM_ORG_STATUS_UPDATED",
			ResourceType:   "ORGANIZATION",
			ResourceID:     &org.ID,
			BeforeData:     map[string]any{"status": oldStatus},
			AfterData:      map[string]any{"status": newStatus},
			Reason:         reason,
			IPAddress:      ip,
			UserAgent:      ua,
		})
	})
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.TenantSubdomainKey(org.Subdomain)).Err()
	}

	org.Status = newStatus
	org.StatusReason = &reason
	return &org, nil
}

func (s *Service) ChangeSubdomain(ctx context.Context, orgID uuid.UUID, newSubdomain, reason string, redirectUntil *time.Time, actorID uuid.UUID, ip, ua string) (*Organization, error) {
	newSubdomain = strings.ToLower(strings.TrimSpace(newSubdomain))
	if !subdomainRegex.MatchString(newSubdomain) {
		return nil, ErrSubdomainInvalidFormat
	}
	var org Organization
	if err := s.db.WithContext(ctx).First(&org, "id = ?", orgID).Error; err != nil {
		return nil, ErrOrgNotFound
	}
	if newSubdomain != strings.ToLower(org.Subdomain) {
		if err := s.ensureSubdomainNotOnCloudflare(ctx, newSubdomain); err != nil {
			return nil, err
		}
	}

	oldSubdomain := org.Subdomain
	oldCFRecordID := org.CloudflareDNSRecordID

	if redirectUntil == nil {
		defaultRedirect := time.Now().Add(30 * 24 * time.Hour)
		redirectUntil = &defaultRedirect
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.validateSubdomain(tx, newSubdomain, &org.ID); err != nil {
			return err
		}

		if err := tx.Model(&org).Updates(map[string]any{
			"subdomain":                newSubdomain,
			"cloudflare_dns_record_id": nil,
			"dns_status":               "PROVISIONING",
			"dns_last_error":           nil,
			"updated_at":               time.Now(),
		}).Error; err != nil {
			return err
		}

		history := OrganizationSubdomainHistory{
			ID:                    uuid.New(),
			OrganizationID:        org.ID,
			OldSubdomain:          oldSubdomain,
			NewSubdomain:          newSubdomain,
			ApprovedBy:            actorID,
			Reason:                reason,
			RedirectUntil:         redirectUntil,
			OldCloudflareRecordID: oldCFRecordID,
			OldRecordStatus:       "REDIRECTING",
		}
		if err := tx.Create(&history).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &actorID,
			OrganizationID: &org.ID,
			Action:         "ORG_SUBDOMAIN_CHANGED",
			ResourceType:   "ORGANIZATION",
			ResourceID:     &org.ID,
			BeforeData: map[string]any{
				"subdomain":                oldSubdomain,
				"cloudflare_dns_record_id": oldCFRecordID,
			},
			AfterData: map[string]any{
				"subdomain":      newSubdomain,
				"dns_status":     "PROVISIONING",
				"redirect_until": redirectUntil,
			},
			Reason:    reason,
			IPAddress: ip,
			UserAgent: ua,
		})
	})
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		pipe := s.rdb.Pipeline()
		pipe.Del(ctx, rediskeys.TenantSubdomainKey(oldSubdomain))
		pipe.Del(ctx, rediskeys.TenantSubdomainKey(newSubdomain))
		_, _ = pipe.Exec(ctx)
	}

	// Đẩy job tạo CNAME mới cho newSubdomain trên Cloudflare
	s.provisioner.EnqueueProvision(org.ID)

	org.Subdomain = newSubdomain
	org.CloudflareDNSRecordID = nil
	org.DNSStatus = "PROVISIONING"
	org.DNSLastError = nil
	return &org, nil
}

func (s *Service) ListReservedSubdomains(ctx context.Context) ([]ReservedSubdomain, error) {
	var items []ReservedSubdomain
	if err := s.db.WithContext(ctx).Order("keyword ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) AddReservedSubdomain(ctx context.Context, keyword, reason string, actorID uuid.UUID, ip, ua string) (*ReservedSubdomain, error) {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	item := &ReservedSubdomain{
		Keyword:   keyword,
		Reason:    strings.TrimSpace(reason),
		CreatedBy: &actorID,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "RESERVED_SUBDOMAIN_CREATED",
			ResourceType: "RESERVED_SUBDOMAIN",
			AfterData:    item,
			Reason:       reason,
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

// SyncReservedSubdomainsFromCloudflare fetches existing DNS records from Cloudflare and inserts
// matching single-label subdomains (not belonging to active organizations) into reserved_subdomains.
func (s *Service) SyncReservedSubdomainsFromCloudflare(ctx context.Context, actorID uuid.UUID, ip, ua string) (map[string]any, error) {
	if s.cf == nil || !s.cf.IsConfigured() {
		return nil, fmt.Errorf("cloudflare chưa được cấu hình")
	}

	records, err := s.cf.ListRecords(ctx)
	if err != nil {
		return nil, err
	}

	var orgSubdomains []string
	s.db.WithContext(ctx).Model(&Organization{}).Pluck("LOWER(subdomain)", &orgSubdomains)
	var redirectSubdomains []string
	s.db.WithContext(ctx).Model(&OrganizationSubdomainHistory{}).
		Where("old_record_status = 'REDIRECTING' AND (redirect_until IS NULL OR redirect_until > ?)", time.Now()).
		Pluck("LOWER(old_subdomain)", &redirectSubdomains)
	var existingReserved []string
	s.db.WithContext(ctx).Model(&ReservedSubdomain{}).Pluck("LOWER(keyword)", &existingReserved)

	skip := make(map[string]bool)
	for _, sub := range orgSubdomains {
		skip[sub] = true
	}
	for _, sub := range redirectSubdomains {
		skip[sub] = true
	}
	for _, kw := range existingReserved {
		skip[kw] = true
	}

	suffix := "." + strings.ToLower(s.cf.RootDomain())
	added := make([]string, 0)

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, rec := range records {
			name := strings.ToLower(strings.TrimSpace(rec.Name))
			if !strings.HasSuffix(name, suffix) {
				continue
			}
			label := strings.TrimSuffix(name, suffix)
			if label == "" || strings.Contains(label, ".") || !subdomainRegex.MatchString(label) {
				continue
			}
			if skip[label] {
				continue
			}
			skip[label] = true

			item := ReservedSubdomain{
				Keyword:   label,
				Reason:    fmt.Sprintf("Synced from Cloudflare DNS (%s: %s)", rec.Type, name),
				CreatedBy: &actorID,
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			added = append(added, label)
		}

		if len(added) > 0 && s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:    "PLATFORM",
				ActorID:      &actorID,
				Action:       "RESERVED_SUBDOMAINS_SYNCED_CLOUDFLARE",
				ResourceType: "RESERVED_SUBDOMAIN",
				AfterData:    map[string]any{"added_keywords": added},
				IPAddress:    ip,
				UserAgent:    ua,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"total_records_scanned": len(records),
		"added_count":           len(added),
		"added_keywords":        added,
	}, nil
}

type SubdomainCheckOutput struct {
	Subdomain         string `json:"subdomain" example:"thpt-chu-van-an"`
	FQDN              string `json:"fqdn" example:"thpt-chu-van-an.nghia196.qzz.io"`
	Valid             bool   `json:"valid" example:"true"`
	Exists            bool   `json:"exists" example:"false"`
	Available         bool   `json:"available" example:"true"`
	CloudflareChecked bool   `json:"cloudflare_checked" example:"true"`
	CloudflareError   string `json:"cloudflare_error,omitempty"`
	Reason            string `json:"reason" example:"AVAILABLE"`
	Message           string `json:"message" example:"Subdomain hợp lệ và chưa tồn tại, có thể sử dụng"`
}

// CheckSubdomainAvailability kiểm tra tính hợp lệ và tồn tại của subdomain:
// 1) Định dạng hợp lệ (regex), 2) Danh sách giữ chỗ (reserved_subdomains),
// 3) Tồn tại trên Cloudflare DNS, 4) Đã thuộc tổ chức hoặc đang redirect trong DB.
func (s *Service) CheckSubdomainAvailability(ctx context.Context, subdomain string) (*SubdomainCheckOutput, error) {
	subdomain = strings.ToLower(strings.TrimSpace(subdomain))
	rootDomain := "school-erp.vn"
	if s.cf != nil {
		rootDomain = s.cf.RootDomain()
	}
	fqdn := fmt.Sprintf("%s.%s", subdomain, rootDomain)

	// 1. Kiểm tra định dạng hợp lệ
	if !subdomainRegex.MatchString(subdomain) {
		return &SubdomainCheckOutput{
			Subdomain: subdomain,
			FQDN:      fqdn,
			Valid:     false,
			Exists:    false,
			Available: false,
			Reason:    "INVALID_FORMAT",
			Message:   "Subdomain không hợp lệ (phải dài 3-40 ký tự, chỉ gồm a-z, 0-9, '-' và không bắt đầu/kết thúc bằng '-')",
		}, nil
	}

	// 2. Kiểm tra danh sách giữ chỗ trước (reserved_subdomains)
	var reservedCount int64
	if err := s.db.WithContext(ctx).Model(&ReservedSubdomain{}).Where("LOWER(keyword) = ?", subdomain).Count(&reservedCount).Error; err != nil {
		return nil, err
	}
	if reservedCount > 0 {
		return &SubdomainCheckOutput{
			Subdomain: subdomain,
			FQDN:      fqdn,
			Valid:     true,
			Exists:    true,
			Available: false,
			Reason:    "RESERVED_KEYWORD",
			Message:   "Subdomain nằm trong danh sách từ khóa giữ chỗ của hệ thống",
		}, nil
	}

	// 3. Nếu không có trong giữ chỗ -> kiểm tra trực tiếp trên Cloudflare DNS
	cfChecked := false
	var cfErrMsg string
	if s.cf != nil && s.cf.IsConfigured() {
		rec, err := s.cf.FindRecordByFQDN(ctx, fqdn)
		if err == nil && rec != nil {
			return &SubdomainCheckOutput{
				Subdomain:         subdomain,
				FQDN:              fqdn,
				Valid:             true,
				Exists:            true,
				Available:         false,
				CloudflareChecked: true,
				Reason:            "EXISTS_ON_CLOUDFLARE",
				Message:           "Subdomain đã tồn tại bản ghi DNS trên Cloudflare",
			}, nil
		}
		if errors.Is(err, cloudflare.ErrRecordNotFound) {
			cfChecked = true
		} else if err != nil {
			cfErrMsg = err.Error()
		}
	}

	// 4. Kiểm tra trong bảng organizations (VD: tổ chức đang PENDING_APPROVAL chưa tạo DNS)
	var orgCount int64
	if err := s.db.WithContext(ctx).Model(&Organization{}).Where("LOWER(subdomain) = ?", subdomain).Count(&orgCount).Error; err != nil {
		return nil, err
	}
	if orgCount > 0 {
		return &SubdomainCheckOutput{
			Subdomain:         subdomain,
			FQDN:              fqdn,
			Valid:             true,
			Exists:            true,
			Available:         false,
			CloudflareChecked: cfChecked,
			CloudflareError:   cfErrMsg,
			Reason:            "TAKEN_BY_ORGANIZATION",
			Message:           "Subdomain đã tồn tại và thuộc về một Tổ chức khác",
		}, nil
	}

	// 5. Kiểm tra trong lịch sử đổi subdomain đang còn hiệu lực redirect
	var redirectCount int64
	if err := s.db.WithContext(ctx).Model(&OrganizationSubdomainHistory{}).
		Where("LOWER(old_subdomain) = ? AND old_record_status = 'REDIRECTING' AND (redirect_until IS NULL OR redirect_until > ?)", subdomain, time.Now()).
		Count(&redirectCount).Error; err != nil {
		return nil, err
	}
	if redirectCount > 0 {
		return &SubdomainCheckOutput{
			Subdomain:         subdomain,
			FQDN:              fqdn,
			Valid:             true,
			Exists:            true,
			Available:         false,
			CloudflareChecked: cfChecked,
			CloudflareError:   cfErrMsg,
			Reason:            "ACTIVE_REDIRECT_HISTORY",
			Message:           "Subdomain đang trong thời gian chuyển hướng (Redirect 301/308) từ một Tổ chức cũ",
		}, nil
	}

	return &SubdomainCheckOutput{
		Subdomain:         subdomain,
		FQDN:              fqdn,
		Valid:             true,
		Exists:            false,
		Available:         true,
		CloudflareChecked: cfChecked,
		CloudflareError:   cfErrMsg,
		Reason:            "AVAILABLE",
		Message:           "Subdomain hợp lệ và chưa tồn tại, có thể sử dụng",
	}, nil
}

// RetryOrganizationDNS kích hoạt lại tiến trình đồng bộ DNS lên Cloudflare cho một Organization.
func (s *Service) RetryOrganizationDNS(ctx context.Context, orgID, actorID uuid.UUID, ip, ua string) (*Organization, error) {
	var org Organization
	if err := s.db.WithContext(ctx).First(&org, "id = ?", orgID).Error; err != nil {
		return nil, ErrOrgNotFound
	}

	oldDNSStatus := org.DNSStatus
	now := time.Now()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&org).Updates(map[string]any{
			"dns_status":     "PROVISIONING",
			"dns_last_error": nil,
			"updated_at":     now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &actorID,
			OrganizationID: &org.ID,
			Action:         "CLOUDFLARE_DNS_RETRY_REQUESTED",
			ResourceType:   "ORGANIZATION",
			ResourceID:     &org.ID,
			BeforeData:     map[string]any{"dns_status": oldDNSStatus, "dns_last_error": org.DNSLastError},
			AfterData:      map[string]any{"dns_status": "PROVISIONING"},
			IPAddress:      ip,
			UserAgent:      ua,
		})
	})
	if err != nil {
		return nil, err
	}

	s.provisioner.EnqueueProvision(org.ID)

	org.DNSStatus = "PROVISIONING"
	org.DNSLastError = nil
	return &org, nil
}

// DeleteRedirectHistoryDNS ngắt redirect sớm và xóa ngay bản ghi DNS của subdomain cũ trên Cloudflare.
func (s *Service) DeleteRedirectHistoryDNS(ctx context.Context, historyID, actorID uuid.UUID, ip, ua string) (*OrganizationSubdomainHistory, error) {
	var history OrganizationSubdomainHistory
	if err := s.db.WithContext(ctx).First(&history, "id = ?", historyID).Error; err != nil {
		return nil, ErrHistoryNotFound
	}

	if history.OldCloudflareRecordID != nil && *history.OldCloudflareRecordID != "" && s.cf != nil && s.cf.IsConfigured() {
		if err := s.cf.DeleteRecord(ctx, *history.OldCloudflareRecordID); err != nil {
			return nil, err
		}
	}

	now := time.Now()
	oldStatus := history.OldRecordStatus

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&history).Updates(map[string]any{
			"old_record_status": "DELETED",
			"cleaned_up_at":     now,
			"redirect_until":    now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &actorID,
			OrganizationID: &history.OrganizationID,
			Action:         "SUBDOMAIN_REDIRECT_TERMINATED",
			ResourceType:   "ORGANIZATION_SUBDOMAIN_HISTORY",
			ResourceID:     &history.ID,
			BeforeData:     map[string]any{"old_subdomain": history.OldSubdomain, "old_record_status": oldStatus},
			AfterData:      map[string]any{"old_subdomain": history.OldSubdomain, "old_record_status": "DELETED", "cleaned_up_at": now},
			IPAddress:      ip,
			UserAgent:      ua,
		})
	})
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.TenantSubdomainKey(history.OldSubdomain)).Err()
	}

	history.OldRecordStatus = "DELETED"
	history.CleanedUpAt = &now
	history.RedirectUntil = &now
	return &history, nil
}

// ResolveTenantBySubdomain xác định trạng thái của một subdomain: ACTIVE, SUSPENDED, REDIRECT, hoặc NOT_FOUND (có cache Redis 30m).
func (s *Service) ResolveTenantBySubdomain(ctx context.Context, subdomain string) (map[string]any, error) {
	subdomain = strings.ToLower(strings.TrimSpace(subdomain))
	cacheKey := rediskeys.TenantSubdomainKey(subdomain)

	if s.rdb != nil {
		cached, err := s.rdb.Get(ctx, cacheKey).Result()
		if err == nil && cached != "" {
			var res map[string]any
			if err := json.Unmarshal([]byte(cached), &res); err == nil {
				return res, nil
			}
		}
	}

	// 1. Tra bảng organizations
	var org Organization
	err := s.db.WithContext(ctx).Where("LOWER(subdomain) = ?", subdomain).First(&org).Error
	if err == nil {
		res := map[string]any{
			"action":          org.Status, // ACTIVE, SUSPENDED, PENDING_APPROVAL
			"organization_id": org.ID,
			"code":            org.Code,
			"name":            org.Name,
			"subdomain":       org.Subdomain,
			"dns_status":      org.DNSStatus,
		}
		if s.rdb != nil {
			if raw, err := json.Marshal(res); err == nil {
				_ = s.rdb.Set(ctx, cacheKey, string(raw), rediskeys.TTLTenantSubdomain).Err()
			}
		}
		return res, nil
	}

	// 2. Tra bảng organization_subdomain_histories (Redirect 301/308)
	var history OrganizationSubdomainHistory
	err = s.db.WithContext(ctx).
		Where("LOWER(old_subdomain) = ? AND old_record_status = 'REDIRECTING' AND (redirect_until IS NULL OR redirect_until > ?)", subdomain, time.Now()).
		Order("created_at DESC").
		First(&history).Error
	if err == nil {
		targetSubdomain := history.NewSubdomain
		var currentOrg Organization
		if err := s.db.WithContext(ctx).First(&currentOrg, "id = ?", history.OrganizationID).Error; err == nil {
			targetSubdomain = currentOrg.Subdomain
		}

		rootDomain := "school-erp.vn"
		if s.cf != nil {
			rootDomain = s.cf.RootDomain()
		}

		res := map[string]any{
			"action":          "REDIRECT",
			"organization_id": history.OrganizationID,
			"old_subdomain":   subdomain,
			"new_subdomain":   targetSubdomain,
			"target_url":      fmt.Sprintf("https://%s.%s", targetSubdomain, rootDomain),
			"redirect_until":  history.RedirectUntil,
		}
		if s.rdb != nil {
			ttl := rediskeys.TTLTenantSubdomain
			if history.RedirectUntil != nil {
				rem := time.Until(*history.RedirectUntil)
				if rem > 0 && rem < ttl {
					ttl = rem
				}
			}
			if raw, err := json.Marshal(res); err == nil {
				_ = s.rdb.Set(ctx, cacheKey, string(raw), ttl).Err()
			}
		}
		return res, nil
	}

	return map[string]any{
		"action":    "NOT_FOUND",
		"subdomain": subdomain,
	}, nil
}
