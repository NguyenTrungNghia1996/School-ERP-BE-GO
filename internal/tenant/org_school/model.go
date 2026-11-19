package org_school

import (
	"encoding/json"
	"school-erp/internal/platform/billing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Organization struct {
	ID           uuid.UUID       `json:"id" gorm:"type:uuid;primaryKey"`
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	Subdomain    string          `json:"subdomain"`
	LogoURL      *string         `json:"logo_url"`
	ContactEmail *string         `json:"contact_email"`
	ContactPhone *string         `json:"contact_phone"`
	Address      *string         `json:"address"`
	Status       string          `json:"status"`
	Settings     json.RawMessage `json:"settings" gorm:"type:jsonb" swaggertype:"object"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func (Organization) TableName() string {
	return "organizations"
}

type School struct {
	ID              uuid.UUID       `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID  uuid.UUID       `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	Code            string          `json:"code" gorm:"column:code;type:varchar(50);not null"`
	Name            string          `json:"name" gorm:"column:name;type:varchar(200);not null"`
	EducationLevels json.RawMessage `json:"education_levels" gorm:"column:education_levels;type:jsonb;not null;default:'[]'" swaggertype:"array,string"`
	Phone           *string         `json:"phone"`
	Email           *string         `json:"email"`
	Address         *string         `json:"address"`
	LogoURL         *string         `json:"logo_url"`
	IsMain          bool            `json:"is_main"`
	Status          string          `json:"status" gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	DeletedAt       gorm.DeletedAt  `json:"-" gorm:"column:deleted_at;index"`
}

func (School) TableName() string {
	return "schools"
}

type OrganizationOverviewOutput struct {
	Organization Organization                     `json:"organization"`
	Entitlement  *billing.OrganizationEntitlement `json:"entitlement,omitempty"`
	SchoolCount  int64                            `json:"school_count"`
	MaxSchools   int64                            `json:"max_schools"`
}

type UpdateOrganizationInput struct {
	Name         *string        `json:"name" example:"Hệ thống Trường Chu Văn An"`
	ContactEmail *string        `json:"contact_email" example:"contact@chuvanan.edu.vn"`
	ContactPhone *string        `json:"contact_phone" example:"02438889999"`
	Address      *string        `json:"address" example:"Số 10 Thụy Khuê, Tây Hồ, Hà Nội"`
	LogoURL      *string        `json:"logo_url"`
	Settings     map[string]any `json:"settings"`
}

type CreateSchoolInput struct {
	Code            string   `json:"code" example:"CS2"`
	Name            string   `json:"name" example:"Cơ sở 2 - THPT Chu Văn An"`
	EducationLevels []string `json:"education_levels" example:"HIGH_SCHOOL"`
	Phone           *string  `json:"phone" example:"02438889998"`
	Email           *string  `json:"email" example:"cs2@chuvanan.edu.vn"`
	Address         *string  `json:"address"`
	LogoURL         *string  `json:"logo_url"`
}

type UpdateSchoolInput struct {
	Name            *string  `json:"name"`
	EducationLevels []string `json:"education_levels"`
	Phone           *string  `json:"phone"`
	Email           *string  `json:"email"`
	Address         *string  `json:"address"`
	LogoURL         *string  `json:"logo_url"`
	Status          *string  `json:"status" example:"ACTIVE"`
}
