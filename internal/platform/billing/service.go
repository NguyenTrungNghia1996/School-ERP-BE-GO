package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"school-erp/internal/platform/audit"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrPackageNotFound         = errors.New("service package not found")
	ErrPackageCodeTaken        = errors.New("service package code already exists")
	ErrVersionNotFound         = errors.New("package version not found")
	ErrPackageVersionImmutable = errors.New("published or deprecated package version is immutable")
	ErrVersionNotPublished     = errors.New("only PUBLISHED package version can be assigned")
	ErrAddonRequiresFeatures   = errors.New("addon package version must have at least 1 feature before publishing")
	ErrPrimaryOverlap          = errors.New("organization already has an overlapping PRIMARY subscription")
	ErrSubscriptionNotFound    = errors.New("subscription not found")
)

type Service struct {
	db      *gorm.DB
	rdb     *redis.Client
	auditor *audit.Service
}

func NewService(db *gorm.DB, rdb *redis.Client, auditor *audit.Service) *Service {
	s := &Service{
		db:      db,
		rdb:     rdb,
		auditor: auditor,
	}
	go s.expirationWorkerLoop()
	return s
}

func (s *Service) ListPackages(ctx context.Context, packageType string) ([]ServicePackageWithSummary, error) {
	q := s.db.WithContext(ctx).Model(&ServicePackage{}).Preload("Versions", func(db *gorm.DB) *gorm.DB {
		return db.Preload("Features").Order("version_number DESC")
	})
	if packageType != "" {
		q = q.Where("package_type = ?", strings.ToUpper(strings.TrimSpace(packageType)))
	}

	var pkgs []ServicePackage
	if err := q.Order("package_type DESC, code ASC").Find(&pkgs).Error; err != nil {
		return nil, err
	}

	type orgCountRow struct {
		PackageID uuid.UUID `gorm:"column:package_id"`
		Count     int64     `gorm:"column:count"`
	}
	var counts []orgCountRow
	s.db.WithContext(ctx).Raw(`
		SELECT spv.package_id, COUNT(DISTINCT os.organization_id) AS count
		FROM organization_subscriptions os
		JOIN service_package_versions spv ON spv.id = os.package_version_id
		WHERE os.status = 'ACTIVE'
		GROUP BY spv.package_id
	`).Scan(&counts)

	countMap := make(map[uuid.UUID]int64, len(counts))
	for _, c := range counts {
		countMap[c.PackageID] = c.Count
	}

	result := make([]ServicePackageWithSummary, 0, len(pkgs))
	for _, pkg := range pkgs {
		var latestPub *ServicePackageVersion
		for i := range pkg.Versions {
			if pkg.Versions[i].Status == "PUBLISHED" {
				vCopy := pkg.Versions[i]
				latestPub = &vCopy
				break
			}
		}
		result = append(result, ServicePackageWithSummary{
			ServicePackage:         pkg,
			LatestPublishedVersion: latestPub,
			ActiveOrgCount:         countMap[pkg.ID],
		})
	}

	return result, nil
}

func (s *Service) GetPackageDetail(ctx context.Context, packageID uuid.UUID) (*ServicePackage, error) {
	var pkg ServicePackage
	if err := s.db.WithContext(ctx).
		Preload("Versions", func(db *gorm.DB) *gorm.DB {
			return db.Preload("Features").Order("version_number DESC")
		}).
		First(&pkg, "id = ?", packageID).Error; err != nil {
		return nil, ErrPackageNotFound
	}
	return &pkg, nil
}

type CreatePackageInput struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	PackageType string    `json:"package_type"`
	IsPublic    *bool     `json:"is_public"`
	IsActive    *bool     `json:"is_active"`
	ActorID     uuid.UUID `json:"-"`
	IPAddress   string    `json:"-"`
	UserAgent   string    `json:"-"`
}

func (s *Service) CreatePackage(ctx context.Context, input CreatePackageInput) (*ServicePackage, error) {
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	var existing int64
	s.db.WithContext(ctx).Model(&ServicePackage{}).Where("UPPER(code) = ?", code).Count(&existing)
	if existing > 0 {
		return nil, ErrPackageCodeTaken
	}

	isPublic := true
	if input.IsPublic != nil {
		isPublic = *input.IsPublic
	}
	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	pkg := &ServicePackage{
		ID:          uuid.New(),
		Code:        code,
		Name:        strings.TrimSpace(input.Name),
		Description: input.Description,
		PackageType: strings.ToUpper(strings.TrimSpace(input.PackageType)),
		IsPublic:    isPublic,
		IsActive:    isActive,
		CreatedBy:   &input.ActorID,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(pkg).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "SERVICE_PACKAGE_CREATED",
			ResourceType: "SERVICE_PACKAGE",
			ResourceID:   &pkg.ID,
			AfterData:    pkg,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	return pkg, nil
}

type CreateVersionInput struct {
	PackageID          uuid.UUID      `json:"-"`
	CloneFromVersionID *uuid.UUID     `json:"clone_from_version_id"`
	VersionLabel       string         `json:"version_label"`
	BillingCycle       string         `json:"billing_cycle"`
	BasePrice          *float64       `json:"base_price"`
	Currency           string         `json:"currency"`
	FeatureIDs         []uuid.UUID    `json:"feature_ids"`
	Quotas             map[string]any `json:"quotas"`
	ReleaseNotes       *string        `json:"release_notes"`
	ActorID            uuid.UUID      `json:"-"`
	IPAddress          string         `json:"-"`
	UserAgent          string         `json:"-"`
}

func (s *Service) CreatePackageVersion(ctx context.Context, input CreateVersionInput) (*ServicePackageVersion, error) {
	var pkg ServicePackage
	if err := s.db.WithContext(ctx).First(&pkg, "id = ?", input.PackageID).Error; err != nil {
		return nil, ErrPackageNotFound
	}

	quotasBytes := []byte("{}")
	billingCycle := strings.ToUpper(strings.TrimSpace(input.BillingCycle))
	basePrice := float64(0)
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	featureIDs := input.FeatureIDs

	// Nhân bản từ clone_from_version_id nếu được truyền
	if input.CloneFromVersionID != nil && *input.CloneFromVersionID != uuid.Nil {
		var srcVer ServicePackageVersion
		if err := s.db.WithContext(ctx).Preload("Features").First(&srcVer, "id = ?", *input.CloneFromVersionID).Error; err == nil {
			if len(srcVer.Quotas) > 0 {
				quotasBytes = srcVer.Quotas
			}
			if billingCycle == "" {
				billingCycle = srcVer.BillingCycle
			}
			if input.BasePrice == nil {
				basePrice = srcVer.BasePrice
			}
			if currency == "" {
				currency = srcVer.Currency
			}
			if len(featureIDs) == 0 {
				for _, f := range srcVer.Features {
					featureIDs = append(featureIDs, f.ID)
				}
			}
		}
	}

	if input.Quotas != nil {
		if b, err := json.Marshal(input.Quotas); err == nil {
			quotasBytes = b
		}
	}
	if billingCycle == "" {
		billingCycle = "YEARLY"
	}
	if input.BasePrice != nil {
		basePrice = *input.BasePrice
	}
	if currency == "" {
		currency = "VND"
	}

	var ver ServicePackageVersion
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var maxVer int
		tx.Raw("SELECT COALESCE(MAX(version_number), 0) FROM service_package_versions WHERE package_id = ?", input.PackageID).Scan(&maxVer)

		nextNum := maxVer + 1
		label := strings.TrimSpace(input.VersionLabel)
		if label == "" {
			label = fmt.Sprintf("v%d.0", nextNum)
		}

		ver = ServicePackageVersion{
			ID:            uuid.New(),
			PackageID:     input.PackageID,
			VersionNumber: nextNum,
			VersionLabel:  label,
			Status:        "DRAFT",
			BillingCycle:  billingCycle,
			BasePrice:     basePrice,
			Currency:      currency,
			Quotas:        quotasBytes,
			ReleaseNotes:  input.ReleaseNotes,
		}
		if err := tx.Create(&ver).Error; err != nil {
			return err
		}

		seen := make(map[uuid.UUID]bool)
		for _, fid := range featureIDs {
			if seen[fid] {
				continue
			}
			seen[fid] = true
			pf := ServicePackageFeature{
				PackageVersionID: ver.ID,
				FeatureID:        fid,
				FeatureLimits:    []byte("{}"),
			}
			if err := tx.Create(&pf).Error; err != nil {
				return err
			}
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "PACKAGE_VERSION_CREATED",
			ResourceType: "SERVICE_PACKAGE_VERSION",
			ResourceID:   &ver.ID,
			AfterData: map[string]any{
				"version":     ver,
				"feature_ids": featureIDs,
			},
			IPAddress: input.IPAddress,
			UserAgent: input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	_ = s.db.WithContext(ctx).Preload("Features").First(&ver, "id = ?", ver.ID).Error
	return &ver, nil
}

type UpdateVersionInput struct {
	VersionID    uuid.UUID      `json:"-"`
	VersionLabel *string        `json:"version_label"`
	BillingCycle *string        `json:"billing_cycle"`
	BasePrice    *float64       `json:"base_price"`
	Currency     *string        `json:"currency"`
	FeatureIDs   []uuid.UUID    `json:"feature_ids"`
	Quotas       map[string]any `json:"quotas"`
	ReleaseNotes *string        `json:"release_notes"`
	ActorID      uuid.UUID      `json:"-"`
	IPAddress    string         `json:"-"`
	UserAgent    string         `json:"-"`
}

func (s *Service) UpdatePackageVersion(ctx context.Context, input UpdateVersionInput) (*ServicePackageVersion, error) {
	var ver ServicePackageVersion
	if err := s.db.WithContext(ctx).Preload("Features").First(&ver, "id = ?", input.VersionID).Error; err != nil {
		return nil, ErrVersionNotFound
	}

	if ver.Status != "DRAFT" {
		return nil, ErrPackageVersionImmutable
	}

	updates := map[string]any{"updated_at": time.Now()}
	if input.VersionLabel != nil && strings.TrimSpace(*input.VersionLabel) != "" {
		updates["version_label"] = strings.TrimSpace(*input.VersionLabel)
	}
	if input.BillingCycle != nil && strings.TrimSpace(*input.BillingCycle) != "" {
		updates["billing_cycle"] = strings.ToUpper(strings.TrimSpace(*input.BillingCycle))
	}
	if input.BasePrice != nil {
		updates["base_price"] = *input.BasePrice
	}
	if input.Currency != nil && strings.TrimSpace(*input.Currency) != "" {
		updates["currency"] = strings.ToUpper(strings.TrimSpace(*input.Currency))
	}
	if input.Quotas != nil {
		if b, err := json.Marshal(input.Quotas); err == nil {
			updates["quotas"] = b
		}
	}
	if input.ReleaseNotes != nil {
		updates["release_notes"] = input.ReleaseNotes
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ver).Updates(updates).Error; err != nil {
			return err
		}

		if input.FeatureIDs != nil {
			if err := tx.Where("package_version_id = ?", ver.ID).Delete(&ServicePackageFeature{}).Error; err != nil {
				return err
			}
			seen := make(map[uuid.UUID]bool)
			for _, fid := range input.FeatureIDs {
				if seen[fid] {
					continue
				}
				seen[fid] = true
				pf := ServicePackageFeature{
					PackageVersionID: ver.ID,
					FeatureID:        fid,
					FeatureLimits:    []byte("{}"),
				}
				if err := tx.Create(&pf).Error; err != nil {
					return err
				}
			}
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "PACKAGE_VERSION_UPDATED",
			ResourceType: "SERVICE_PACKAGE_VERSION",
			ResourceID:   &ver.ID,
			AfterData: map[string]any{
				"updates":     updates,
				"feature_ids": input.FeatureIDs,
			},
			IPAddress: input.IPAddress,
			UserAgent: input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	_ = s.db.WithContext(ctx).Preload("Features").First(&ver, "id = ?", ver.ID).Error
	return &ver, nil
}

func (s *Service) PublishPackageVersion(ctx context.Context, versionID, actorID uuid.UUID, ip, ua string) (*ServicePackageVersion, error) {
	var ver ServicePackageVersion
	if err := s.db.WithContext(ctx).Preload("Package").Preload("Features").First(&ver, "id = ?", versionID).Error; err != nil {
		return nil, ErrVersionNotFound
	}

	if ver.Status != "DRAFT" {
		return nil, ErrPackageVersionImmutable
	}

	if len(ver.Features) == 0 {
		return nil, ErrAddonRequiresFeatures
	}

	now := time.Now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ver).Updates(map[string]any{
			"status":       "PUBLISHED",
			"published_at": now,
			"published_by": actorID,
			"updated_at":   now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "PACKAGE_VERSION_PUBLISHED",
			ResourceType: "SERVICE_PACKAGE_VERSION",
			ResourceID:   &ver.ID,
			BeforeData:   map[string]any{"status": "DRAFT"},
			AfterData:    map[string]any{"status": "PUBLISHED", "published_at": now, "published_by": actorID},
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
	if err != nil {
		return nil, err
	}

	ver.Status = "PUBLISHED"
	ver.PublishedAt = &now
	ver.PublishedBy = &actorID
	return &ver, nil
}

func (s *Service) DeprecatePackageVersion(ctx context.Context, versionID, actorID uuid.UUID, ip, ua string) (*ServicePackageVersion, error) {
	var ver ServicePackageVersion
	if err := s.db.WithContext(ctx).Preload("Features").First(&ver, "id = ?", versionID).Error; err != nil {
		return nil, ErrVersionNotFound
	}

	if ver.Status != "PUBLISHED" {
		return nil, ErrPackageVersionImmutable
	}

	oldStatus := ver.Status
	now := time.Now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ver).Updates(map[string]any{
			"status":     "DEPRECATED",
			"updated_at": now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "PACKAGE_VERSION_DEPRECATED",
			ResourceType: "SERVICE_PACKAGE_VERSION",
			ResourceID:   &ver.ID,
			BeforeData:   map[string]any{"status": oldStatus},
			AfterData:    map[string]any{"status": "DEPRECATED"},
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
	if err != nil {
		return nil, err
	}

	ver.Status = "DEPRECATED"
	return &ver, nil
}

func (s *Service) ListOrgSubscriptions(ctx context.Context, orgID uuid.UUID) ([]OrganizationSubscription, error) {
	var subs []OrganizationSubscription
	if err := s.db.WithContext(ctx).
		Preload("PackageVersion.Package").
		Preload("PackageVersion.Features").
		Where("organization_id = ?", orgID).
		Order("created_at DESC").
		Find(&subs).Error; err != nil {
		return nil, err
	}
	return subs, nil
}

type CreateSubscriptionInput struct {
	OrganizationID   uuid.UUID      `json:"-"`
	PackageVersionID uuid.UUID      `json:"package_version_id"`
	StartedAt        time.Time      `json:"started_at"`
	ExpiresAt        time.Time      `json:"expires_at"`
	CustomQuotas     map[string]any `json:"custom_quotas"`
	AutoRenew        bool           `json:"auto_renew"`
	Notes            *string        `json:"notes"`
	ActorID          uuid.UUID      `json:"-"`
	IPAddress        string         `json:"-"`
	UserAgent        string         `json:"-"`
}

func isExclusionOverlapError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23P01") || strings.Contains(msg, "excl_org_primary_subscription_overlap")
}

func (s *Service) CreateSubscription(ctx context.Context, input CreateSubscriptionInput) (*OrganizationSubscription, error) {
	var ver ServicePackageVersion
	if err := s.db.WithContext(ctx).Preload("Package").First(&ver, "id = ?", input.PackageVersionID).Error; err != nil {
		return nil, ErrVersionNotFound
	}
	if ver.Status != "PUBLISHED" {
		return nil, ErrVersionNotPublished
	}
	if ver.Package == nil {
		return nil, ErrPackageNotFound
	}

	var customQuotasBytes json.RawMessage
	if input.CustomQuotas != nil {
		if b, err := json.Marshal(input.CustomQuotas); err == nil {
			customQuotasBytes = b
		}
	}

	now := time.Now()
	status := "ACTIVE"
	if input.StartedAt.After(now) {
		status = "SCHEDULED"
	}

	var sub OrganizationSubscription
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_ = tx.Exec("SET LOCAL app.organization_id = ?", input.OrganizationID.String()).Error

		if ver.Package.PackageType == "PRIMARY" {
			var overlapCount int64
			if err := tx.Model(&OrganizationSubscription{}).
				Where("organization_id = ? AND subscription_type = 'PRIMARY' AND status IN ('SCHEDULED', 'ACTIVE', 'SUSPENDED')", input.OrganizationID).
				Where("started_at < ? AND expires_at > ?", input.ExpiresAt, input.StartedAt).
				Count(&overlapCount).Error; err != nil {
				return err
			}
			if overlapCount > 0 {
				return ErrPrimaryOverlap
			}
		}

		sub = OrganizationSubscription{
			ID:               uuid.New(),
			OrganizationID:   input.OrganizationID,
			PackageVersionID: ver.ID,
			SubscriptionType: ver.Package.PackageType,
			Status:           status,
			StartedAt:        input.StartedAt,
			ExpiresAt:        input.ExpiresAt,
			CustomQuotas:     customQuotasBytes,
			AutoRenew:        input.AutoRenew,
			Notes:            input.Notes,
			AssignedBy:       &input.ActorID,
		}
		if err := tx.Create(&sub).Error; err != nil {
			if isExclusionOverlapError(err) {
				return ErrPrimaryOverlap
			}
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &input.ActorID,
			OrganizationID: &input.OrganizationID,
			Action:         "SUBSCRIPTION_CREATED",
			ResourceType:   "ORGANIZATION_SUBSCRIPTION",
			ResourceID:     &sub.ID,
			AfterData:      sub,
			IPAddress:      input.IPAddress,
			UserAgent:      input.UserAgent,
		})
	})
	if err != nil {
		if isExclusionOverlapError(err) {
			return nil, ErrPrimaryOverlap
		}
		return nil, err
	}

	s.InvalidateOrgEntitlementCache(ctx, input.OrganizationID)
	return &sub, nil
}

type RenewSubscriptionInput struct {
	SubscriptionID   uuid.UUID      `json:"-"`
	PackageVersionID *uuid.UUID     `json:"package_version_id"`
	NewExpiresAt     time.Time      `json:"new_expires_at"`
	CustomQuotas     map[string]any `json:"custom_quotas"`
	Notes            *string        `json:"notes"`
	ActorID          uuid.UUID      `json:"-"`
	IPAddress        string         `json:"-"`
	UserAgent        string         `json:"-"`
}

func (s *Service) RenewSubscription(ctx context.Context, input RenewSubscriptionInput) (*OrganizationSubscription, error) {
	var oldSub OrganizationSubscription
	if err := s.db.WithContext(ctx).First(&oldSub, "id = ?", input.SubscriptionID).Error; err != nil {
		return nil, ErrSubscriptionNotFound
	}

	targetVerID := oldSub.PackageVersionID
	if input.PackageVersionID != nil && *input.PackageVersionID != uuid.Nil {
		targetVerID = *input.PackageVersionID
	}

	var ver ServicePackageVersion
	if err := s.db.WithContext(ctx).First(&ver, "id = ?", targetVerID).Error; err != nil {
		return nil, ErrVersionNotFound
	}
	if ver.Status != "PUBLISHED" {
		return nil, ErrVersionNotPublished
	}

	startedAt := oldSub.ExpiresAt
	if time.Now().After(startedAt) {
		startedAt = time.Now()
	}
	if !input.NewExpiresAt.After(startedAt) {
		return nil, fmt.Errorf("new_expires_at must be after %s", startedAt.Format(time.RFC3339))
	}

	// Nếu cùng package_version_id và gói hiện tại đang ACTIVE -> cộng dồn trực tiếp expires_at hoặc tạo bản ghi nối tiếp
	if targetVerID == oldSub.PackageVersionID && oldSub.Status == "ACTIVE" {
		oldExp := oldSub.ExpiresAt
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			_ = tx.Exec("SET LOCAL app.organization_id = ?", oldSub.OrganizationID.String()).Error

			updates := map[string]any{
				"expires_at": input.NewExpiresAt,
				"updated_at": time.Now(),
			}
			if input.Notes != nil {
				updates["notes"] = input.Notes
			}
			if err := tx.Model(&oldSub).Updates(updates).Error; err != nil {
				if isExclusionOverlapError(err) {
					return ErrPrimaryOverlap
				}
				return err
			}

			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "PLATFORM",
				ActorID:        &input.ActorID,
				OrganizationID: &oldSub.OrganizationID,
				Action:         "SUBSCRIPTION_RENEWED",
				ResourceType:   "ORGANIZATION_SUBSCRIPTION",
				ResourceID:     &oldSub.ID,
				BeforeData:     map[string]any{"expires_at": oldExp},
				AfterData:      map[string]any{"expires_at": input.NewExpiresAt},
				IPAddress:      input.IPAddress,
				UserAgent:      input.UserAgent,
			})
		})
		if err != nil {
			return nil, err
		}
		oldSub.ExpiresAt = input.NewExpiresAt
		s.InvalidateOrgEntitlementCache(ctx, oldSub.OrganizationID)
		return &oldSub, nil
	}

	return s.CreateSubscription(ctx, CreateSubscriptionInput{
		OrganizationID:   oldSub.OrganizationID,
		PackageVersionID: targetVerID,
		StartedAt:        startedAt,
		ExpiresAt:        input.NewExpiresAt,
		CustomQuotas:     input.CustomQuotas,
		AutoRenew:        oldSub.AutoRenew,
		Notes:            input.Notes,
		ActorID:          input.ActorID,
		IPAddress:        input.IPAddress,
		UserAgent:        input.UserAgent,
	})
}

type SwitchPrimaryInput struct {
	OrganizationID   uuid.UUID      `json:"-"`
	PackageVersionID uuid.UUID      `json:"package_version_id"`
	NewExpiresAt     time.Time      `json:"new_expires_at"`
	CustomQuotas     map[string]any `json:"custom_quotas"`
	Notes            *string        `json:"notes"`
	Reason           string         `json:"reason"`
	ActorID          uuid.UUID      `json:"-"`
	IPAddress        string         `json:"-"`
	UserAgent        string         `json:"-"`
}

func (s *Service) SwitchPrimaryPackage(ctx context.Context, input SwitchPrimaryInput) (*OrganizationSubscription, error) {
	var ver ServicePackageVersion
	if err := s.db.WithContext(ctx).Preload("Package").First(&ver, "id = ?", input.PackageVersionID).Error; err != nil {
		return nil, ErrVersionNotFound
	}
	if ver.Status != "PUBLISHED" || ver.Package == nil || ver.Package.PackageType != "PRIMARY" {
		return nil, ErrVersionNotPublished
	}

	now := time.Now()
	if !input.NewExpiresAt.After(now) {
		return nil, fmt.Errorf("new_expires_at must be in the future")
	}

	var customQuotasBytes json.RawMessage
	if input.CustomQuotas != nil {
		if b, err := json.Marshal(input.CustomQuotas); err == nil {
			customQuotasBytes = b
		}
	}

	var newSub OrganizationSubscription
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_ = tx.Exec("SET LOCAL app.organization_id = ?", input.OrganizationID.String()).Error

		// 1. Kết thúc gói chính cũ tại thời điểm NOW()
		var activePrimaries []OrganizationSubscription
		if err := tx.Where("organization_id = ? AND subscription_type = 'PRIMARY' AND status IN ('ACTIVE', 'SCHEDULED', 'SUSPENDED')", input.OrganizationID).
			Find(&activePrimaries).Error; err != nil {
			return err
		}

		for _, old := range activePrimaries {
			endTime := now
			if !endTime.After(old.StartedAt) {
				endTime = old.StartedAt.Add(1 * time.Millisecond)
			}
			if err := tx.Model(&OrganizationSubscription{}).Where("id = ?", old.ID).Updates(map[string]any{
				"expires_at": endTime,
				"status":     "EXPIRED",
				"updated_at": now,
			}).Error; err != nil {
				return err
			}
		}

		// 2. Tạo bản ghi gói chính mới bắt đầu từ NOW() đến new_expires_at
		newSub = OrganizationSubscription{
			ID:               uuid.New(),
			OrganizationID:   input.OrganizationID,
			PackageVersionID: ver.ID,
			SubscriptionType: "PRIMARY",
			Status:           "ACTIVE",
			StartedAt:        now.Add(2 * time.Millisecond),
			ExpiresAt:        input.NewExpiresAt,
			CustomQuotas:     customQuotasBytes,
			Notes:            input.Notes,
			AssignedBy:       &input.ActorID,
		}
		if err := tx.Create(&newSub).Error; err != nil {
			if isExclusionOverlapError(err) {
				return ErrPrimaryOverlap
			}
			return err
		}

		// 3. Ghi Audit Log
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &input.ActorID,
			OrganizationID: &input.OrganizationID,
			Action:         "PRIMARY_SUBSCRIPTION_SWITCHED",
			ResourceType:   "ORGANIZATION_SUBSCRIPTION",
			ResourceID:     &newSub.ID,
			BeforeData:     activePrimaries,
			AfterData:      newSub,
			Reason:         input.Reason,
			IPAddress:      input.IPAddress,
			UserAgent:      input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	s.InvalidateOrgEntitlementCache(ctx, input.OrganizationID)
	return &newSub, nil
}

func (s *Service) SuspendSubscription(ctx context.Context, subID uuid.UUID, reason string, actorID uuid.UUID, ip, ua string) (*OrganizationSubscription, error) {
	var sub OrganizationSubscription
	if err := s.db.WithContext(ctx).First(&sub, "id = ?", subID).Error; err != nil {
		return nil, ErrSubscriptionNotFound
	}

	oldStatus := sub.Status
	now := time.Now()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_ = tx.Exec("SET LOCAL app.organization_id = ?", sub.OrganizationID.String()).Error

		if err := tx.Model(&sub).Updates(map[string]any{
			"status":     "SUSPENDED",
			"notes":      reason,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &actorID,
			OrganizationID: &sub.OrganizationID,
			Action:         "SUBSCRIPTION_SUSPENDED",
			ResourceType:   "ORGANIZATION_SUBSCRIPTION",
			ResourceID:     &sub.ID,
			BeforeData:     map[string]any{"status": oldStatus},
			AfterData:      map[string]any{"status": "SUSPENDED"},
			Reason:         reason,
			IPAddress:      ip,
			UserAgent:      ua,
		})
	})
	if err != nil {
		return nil, err
	}

	s.InvalidateOrgEntitlementCache(ctx, sub.OrganizationID)
	sub.Status = "SUSPENDED"
	return &sub, nil
}

func (s *Service) RevokeSubscription(ctx context.Context, subID uuid.UUID, reason string, actorID uuid.UUID, ip, ua string) (*OrganizationSubscription, error) {
	var sub OrganizationSubscription
	if err := s.db.WithContext(ctx).First(&sub, "id = ?", subID).Error; err != nil {
		return nil, ErrSubscriptionNotFound
	}

	oldStatus := sub.Status
	now := time.Now()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_ = tx.Exec("SET LOCAL app.organization_id = ?", sub.OrganizationID.String()).Error

		if err := tx.Model(&sub).Updates(map[string]any{
			"status":        "REVOKED",
			"revoked_at":    now,
			"revoked_by":    actorID,
			"revoke_reason": reason,
			"updated_at":    now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:      "PLATFORM",
			ActorID:        &actorID,
			OrganizationID: &sub.OrganizationID,
			Action:         "SUBSCRIPTION_REVOKED",
			ResourceType:   "ORGANIZATION_SUBSCRIPTION",
			ResourceID:     &sub.ID,
			BeforeData:     map[string]any{"status": oldStatus},
			AfterData:      map[string]any{"status": "REVOKED", "revoked_at": now},
			Reason:         reason,
			IPAddress:      ip,
			UserAgent:      ua,
		})
	})
	if err != nil {
		return nil, err
	}

	s.InvalidateOrgEntitlementCache(ctx, sub.OrganizationID)

	sub.Status = "REVOKED"
	sub.RevokedAt = &now
	sub.RevokedBy = &actorID
	sub.RevokeReason = &reason
	return &sub, nil
}
