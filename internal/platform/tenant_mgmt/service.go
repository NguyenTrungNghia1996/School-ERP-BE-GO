package tenant_mgmt

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
	ErrSubdomainInvalidFormat   = errors.New("invalid subdomain format")
	ErrSubdomainReservedOrTaken = errors.New("subdomain is reserved or already taken")
	ErrOrgNotPending            = errors.New("organization is not in PENDING_APPROVAL status")
)

// Subdomain lowercase, a-z, 0-9, -, dài 3-40 ký tự, không bắt đầu/kết thúc bằng dấu gạch ngang
var subdomainRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

type Service struct {
	db      *gorm.DB
	rdb     *redis.Client
	auditor *audit.Service
}

func NewService(db *gorm.DB, rdb *redis.Client, auditor *audit.Service) *Service {
	return &Service{
		db:      db,
		rdb:     rdb,
		auditor: auditor,
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

	return nil
}

type ListOrgsFilter struct {
	Search string
	Status string
	Page   int
	Limit  int
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
			JOIN service_packages sp ON sp.id = os.package_id
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
		SELECT os.id, os.package_id, sp.code AS package_code, sp.name AS package_name,
		       os.package_version_id, spv.version AS package_version,
		       os.subscription_type, os.status, os.started_at, os.expires_at,
		       os.revoked_at, os.revoke_reason, os.created_at
		FROM organization_subscriptions os
		JOIN service_packages sp ON sp.id = os.package_id
		JOIN service_package_versions spv ON spv.id = os.package_version_id
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
			"status":      "ACTIVE",
			"approved_by": actorID,
			"approved_at": now,
			"updated_at":  now,
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
			AfterData:      map[string]any{"status": "ACTIVE", "approved_by": actorID},
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

	org.Status = "ACTIVE"
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
	var org Organization
	if err := s.db.WithContext(ctx).First(&org, "id = ?", orgID).Error; err != nil {
		return nil, ErrOrgNotFound
	}

	oldSubdomain := org.Subdomain

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.validateSubdomain(tx, newSubdomain, &org.ID); err != nil {
			return err
		}

		if err := tx.Model(&org).Updates(map[string]any{
			"subdomain":  newSubdomain,
			"updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}

		history := OrganizationSubdomainHistory{
			ID:             uuid.New(),
			OrganizationID: org.ID,
			OldSubdomain:   oldSubdomain,
			NewSubdomain:   newSubdomain,
			ApprovedBy:     actorID,
			Reason:         reason,
			RedirectUntil:  redirectUntil,
		}
		if err := tx.Create(&history).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &actorID,
			OrganizationID: &org.ID,
			Action:         "PLATFORM_ORG_SUBDOMAIN_CHANGED",
			ResourceType:   "ORGANIZATION",
			ResourceID:     &org.ID,
			BeforeData:     map[string]any{"subdomain": oldSubdomain},
			AfterData:      map[string]any{"subdomain": newSubdomain},
			Reason:         reason,
			IPAddress:      ip,
			UserAgent:      ua,
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

	org.Subdomain = newSubdomain
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
