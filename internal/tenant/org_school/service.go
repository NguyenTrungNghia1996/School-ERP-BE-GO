package org_school

import (
	"context"
	"encoding/json"
	"errors"
	"school-erp/internal/pkg/database"
	"school-erp/internal/platform/audit"
	"school-erp/internal/platform/billing"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrOrgNotFound          = errors.New("organization not found")
	ErrSchoolNotFound       = errors.New("school not found")
	ErrSchoolCodeTaken      = errors.New("school code already exists in this organization")
	ErrSchoolQuotaExceeded  = errors.New("school count exceeds package max_schools quota")
	ErrInvalidSchoolStatus  = errors.New("invalid school status")
)

type Service struct {
	db         *gorm.DB
	billingSvc *billing.Service
	auditor    *audit.Service
}

func NewService(db *gorm.DB, billingSvc *billing.Service, auditor *audit.Service) *Service {
	return &Service{
		db:         db,
		billingSvc: billingSvc,
		auditor:    auditor,
	}
}

func (s *Service) GetOrganizationOverview(ctx context.Context, tc database.TenantContext) (*OrganizationOverviewOutput, error) {
	orgID := uuid.MustParse(tc.OrganizationID)

	var org Organization
	if err := s.db.WithContext(ctx).First(&org, "id = ? AND deleted_at IS NULL", orgID).Error; err != nil {
		return nil, ErrOrgNotFound
	}

	var ent *billing.OrganizationEntitlement
	var maxSchools int64
	if s.billingSvc != nil {
		if e, err := s.billingSvc.CalculateEntitlement(ctx, orgID); err == nil {
			ent = e
			maxSchools = e.EffectiveQuotas["max_schools"]
		}
	}

	var schoolCount int64
	_ = database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		return tx.Model(&School{}).Where("organization_id = ?", orgID).Count(&schoolCount).Error
	})

	return &OrganizationOverviewOutput{
		Organization: org,
		Entitlement:  ent,
		SchoolCount:  schoolCount,
		MaxSchools:   maxSchools,
	}, nil
}

func (s *Service) UpdateOrganization(ctx context.Context, tc database.TenantContext, input UpdateOrganizationInput, actorID uuid.UUID, ip, ua string) (*Organization, error) {
	orgID := uuid.MustParse(tc.OrganizationID)

	var org Organization
	if err := s.db.WithContext(ctx).First(&org, "id = ? AND deleted_at IS NULL", orgID).Error; err != nil {
		return nil, ErrOrgNotFound
	}

	updates := map[string]any{"updated_at": time.Now()}
	if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
		updates["name"] = strings.TrimSpace(*input.Name)
	}
	if input.ContactEmail != nil {
		updates["contact_email"] = input.ContactEmail
	}
	if input.ContactPhone != nil {
		updates["contact_phone"] = input.ContactPhone
	}
	if input.Address != nil {
		updates["address"] = input.Address
	}
	if input.LogoURL != nil {
		updates["logo_url"] = input.LogoURL
	}
	if input.Settings != nil {
		if raw, err := json.Marshal(input.Settings); err == nil {
			updates["settings"] = raw
		}
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&org).Updates(updates).Error; err != nil {
			return err
		}
		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				Action:         "TENANT_ORG_UPDATED",
				ResourceType:   "ORGANIZATION",
				ResourceID:     &orgID,
				AfterData:      updates,
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	_ = s.db.WithContext(ctx).First(&org, "id = ?", orgID).Error
	return &org, nil
}

func (s *Service) ListSchools(ctx context.Context, tc database.TenantContext) ([]School, error) {
	var schools []School
	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		return tx.Order("is_main DESC, created_at ASC").Find(&schools).Error
	})
	return schools, err
}

func (s *Service) CreateSchool(ctx context.Context, tc database.TenantContext, input CreateSchoolInput, actorID uuid.UUID, ip, ua string) (*School, error) {
	orgID := uuid.MustParse(tc.OrganizationID)
	code := strings.ToUpper(strings.TrimSpace(input.Code))

	var maxSchools int64
	if s.billingSvc != nil {
		if ent, err := s.billingSvc.CalculateEntitlement(ctx, orgID); err == nil {
			maxSchools = ent.EffectiveQuotas["max_schools"]
		}
	}

	levelsRaw := []byte("[]")
	if len(input.EducationLevels) > 0 {
		if b, err := json.Marshal(input.EducationLevels); err == nil {
			levelsRaw = b
		}
	}

	school := &School{
		ID:              uuid.New(),
		OrganizationID:  orgID,
		Code:            code,
		Name:            strings.TrimSpace(input.Name),
		EducationLevels: levelsRaw,
		Phone:           input.Phone,
		Email:           input.Email,
		Address:         input.Address,
		LogoURL:         input.LogoURL,
		IsMain:          false,
		Status:          "ACTIVE",
	}

	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		var currentCount int64
		if err := tx.Model(&School{}).Where("organization_id = ?", orgID).Count(&currentCount).Error; err != nil {
			return err
		}
		if maxSchools > 0 && currentCount >= maxSchools {
			return ErrSchoolQuotaExceeded
		}

		var dupCount int64
		tx.Model(&School{}).Where("organization_id = ? AND UPPER(code) = ?", orgID, code).Count(&dupCount)
		if dupCount > 0 {
			return ErrSchoolCodeTaken
		}

		if err := tx.Create(school).Error; err != nil {
			return err
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				SchoolID:       &school.ID,
				Action:         "TENANT_SCHOOL_CREATED",
				ResourceType:   "SCHOOL",
				ResourceID:     &school.ID,
				AfterData:      school,
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return school, nil
}

func (s *Service) UpdateSchool(ctx context.Context, tc database.TenantContext, schoolID uuid.UUID, input UpdateSchoolInput, actorID uuid.UUID, ip, ua string) (*School, error) {
	orgID := uuid.MustParse(tc.OrganizationID)
	var school School

	updates := map[string]any{"updated_at": time.Now()}
	if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
		updates["name"] = strings.TrimSpace(*input.Name)
	}
	if input.EducationLevels != nil {
		if b, err := json.Marshal(input.EducationLevels); err == nil {
			updates["education_levels"] = b
		}
	}
	if input.Phone != nil {
		updates["phone"] = input.Phone
	}
	if input.Email != nil {
		updates["email"] = input.Email
	}
	if input.Address != nil {
		updates["address"] = input.Address
	}
	if input.LogoURL != nil {
		updates["logo_url"] = input.LogoURL
	}
	if input.Status != nil {
		st := strings.ToUpper(strings.TrimSpace(*input.Status))
		if st != "ACTIVE" && st != "INACTIVE" && st != "SUSPENDED" {
			return nil, ErrInvalidSchoolStatus
		}
		updates["status"] = st
	}

	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		if err := tx.First(&school, "id = ? AND organization_id = ?", schoolID, orgID).Error; err != nil {
			return ErrSchoolNotFound
		}
		if err := tx.Model(&school).Updates(updates).Error; err != nil {
			return err
		}
		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				SchoolID:       &schoolID,
				Action:         "TENANT_SCHOOL_UPDATED",
				ResourceType:   "SCHOOL",
				ResourceID:     &schoolID,
				AfterData:      updates,
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return tx.First(&school, "id = ?", schoolID).Error
	})
	if err != nil {
		return nil, err
	}
	return &school, nil
}
