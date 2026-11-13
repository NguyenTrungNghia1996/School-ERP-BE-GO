package billing

import (
	"context"
	"encoding/json"
	"errors"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/audit"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrPackageNotFound          = errors.New("service package not found")
	ErrPackageCodeTaken         = errors.New("service package code already exists")
	ErrVersionNotFound          = errors.New("package version not found")
	ErrPackageVersionImmutable  = errors.New("published package version is immutable")
	ErrVersionNotPublished      = errors.New("only PUBLISHED package version can be subscribed")
	ErrPrimaryOverlap           = errors.New("organization already has an overlapping ACTIVE PRIMARY subscription")
	ErrSubscriptionNotFound     = errors.New("subscription not found")
)

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

func (s *Service) ListPackages(ctx context.Context, packageType string) ([]ServicePackage, error) {
	q := s.db.WithContext(ctx).Model(&ServicePackage{}).Preload("Versions.Features")
	if packageType != "" {
		q = q.Where("package_type = ?", strings.ToUpper(packageType))
	}
	var pkgs []ServicePackage
	if err := q.Order("package_type DESC, code ASC").Find(&pkgs).Error; err != nil {
		return nil, err
	}
	return pkgs, nil
}

type CreatePackageInput struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	PackageType string    `json:"package_type"`
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
		IsActive:    isActive,
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
	PackageID  uuid.UUID      `json:"-"`
	FeatureIDs []uuid.UUID    `json:"feature_ids"`
	Quotas     map[string]any `json:"quotas"`
	Notes      *string        `json:"notes"`
	ActorID    uuid.UUID      `json:"-"`
	IPAddress  string         `json:"-"`
	UserAgent  string         `json:"-"`
}

func (s *Service) CreatePackageVersion(ctx context.Context, input CreateVersionInput) (*ServicePackageVersion, error) {
	var pkg ServicePackage
	if err := s.db.WithContext(ctx).First(&pkg, "id = ?", input.PackageID).Error; err != nil {
		return nil, ErrPackageNotFound
	}

	quotasBytes := []byte("{}")
	if input.Quotas != nil {
		if b, err := json.Marshal(input.Quotas); err == nil {
			quotasBytes = b
		}
	}

	var ver ServicePackageVersion
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var maxVer int
		tx.Raw("SELECT COALESCE(MAX(version), 0) FROM service_package_versions WHERE package_id = ?", input.PackageID).Scan(&maxVer)

		ver = ServicePackageVersion{
			ID:        uuid.New(),
			PackageID: input.PackageID,
			Version:   maxVer + 1,
			Status:    "DRAFT",
			Quotas:    quotasBytes,
			Notes:     input.Notes,
			CreatedBy: &input.ActorID,
		}
		if err := tx.Create(&ver).Error; err != nil {
			return err
		}

		for _, fid := range input.FeatureIDs {
			pf := ServicePackageFeature{
				PackageVersionID: ver.ID,
				FeatureID:        fid,
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

type UpdateVersionInput struct {
	VersionID  uuid.UUID      `json:"-"`
	FeatureIDs []uuid.UUID    `json:"feature_ids"`
	Quotas     map[string]any `json:"quotas"`
	Notes      *string        `json:"notes"`
	ActorID    uuid.UUID      `json:"-"`
	IPAddress  string         `json:"-"`
	UserAgent  string         `json:"-"`
}

func (s *Service) UpdatePackageVersion(ctx context.Context, input UpdateVersionInput) (*ServicePackageVersion, error) {
	var ver ServicePackageVersion
	if err := s.db.WithContext(ctx).Preload("Features").First(&ver, "id = ?", input.VersionID).Error; err != nil {
		return nil, ErrVersionNotFound
	}

	if ver.Status != "DRAFT" {
		return nil, ErrPackageVersionImmutable
	}

	quotasBytes := ver.Quotas
	if input.Quotas != nil {
		if b, err := json.Marshal(input.Quotas); err == nil {
			quotasBytes = b
		}
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"quotas":     quotasBytes,
			"updated_at": time.Now(),
		}
		if input.Notes != nil {
			updates["notes"] = input.Notes
		}
		if err := tx.Model(&ver).Updates(updates).Error; err != nil {
			return err
		}

		if input.FeatureIDs != nil {
			if err := tx.Where("package_version_id = ?", ver.ID).Delete(&ServicePackageFeature{}).Error; err != nil {
				return err
			}
			for _, fid := range input.FeatureIDs {
				pf := ServicePackageFeature{
					PackageVersionID: ver.ID,
					FeatureID:        fid,
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
				"feature_ids": input.FeatureIDs,
				"quotas":      input.Quotas,
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
	if err := s.db.WithContext(ctx).Preload("Features").First(&ver, "id = ?", versionID).Error; err != nil {
		return nil, ErrVersionNotFound
	}

	if ver.Status == "PUBLISHED" {
		return nil, ErrPackageVersionImmutable
	}

	now := time.Now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ver).Updates(map[string]any{
			"status":       "PUBLISHED",
			"published_at": now,
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
			BeforeData:   map[string]any{"status": ver.Status},
			AfterData:    map[string]any{"status": "PUBLISHED", "published_at": now},
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
	if err != nil {
		return nil, err
	}

	ver.Status = "PUBLISHED"
	ver.PublishedAt = &now
	return &ver, nil
}

type CreateSubscriptionInput struct {
	OrganizationID   uuid.UUID `json:"-"`
	PackageVersionID uuid.UUID `json:"package_version_id"`
	StartedAt        time.Time `json:"started_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	ActorID          uuid.UUID `json:"-"`
	IPAddress        string    `json:"-"`
	UserAgent        string    `json:"-"`
}

func (s *Service) CreateSubscription(ctx context.Context, input CreateSubscriptionInput) (*OrganizationSubscription, error) {
	var ver ServicePackageVersion
	if err := s.db.WithContext(ctx).First(&ver, "id = ?", input.PackageVersionID).Error; err != nil {
		return nil, ErrVersionNotFound
	}
	if ver.Status != "PUBLISHED" {
		return nil, ErrVersionNotPublished
	}

	var pkg ServicePackage
	if err := s.db.WithContext(ctx).First(&pkg, "id = ?", ver.PackageID).Error; err != nil {
		return nil, ErrPackageNotFound
	}

	var sub OrganizationSubscription
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if pkg.PackageType == "PRIMARY" {
			var overlapCount int64
			if err := tx.Model(&OrganizationSubscription{}).
				Where("organization_id = ? AND subscription_type = 'PRIMARY' AND status = 'ACTIVE'", input.OrganizationID).
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
			PackageID:        pkg.ID,
			PackageVersionID: ver.ID,
			SubscriptionType: pkg.PackageType,
			Status:           "ACTIVE",
			StartedAt:        input.StartedAt,
			ExpiresAt:        input.ExpiresAt,
			CreatedBy:        &input.ActorID,
		}
		if err := tx.Create(&sub).Error; err != nil {
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
		return nil, err
	}

	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.TenantEntitlementKey(input.OrganizationID.String())).Err()
	}

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

	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.TenantEntitlementKey(sub.OrganizationID.String())).Err()
	}

	sub.Status = "REVOKED"
	sub.RevokedAt = &now
	sub.RevokedBy = &actorID
	sub.RevokeReason = &reason
	return &sub, nil
}
