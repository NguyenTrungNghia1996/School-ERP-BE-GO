package tenant_mgmt

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Organization struct {
	ID                    uuid.UUID      `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Code                  string         `json:"code" gorm:"column:code;type:varchar(50);not null;unique"`
	Name                  string         `json:"name" gorm:"column:name;type:varchar(200);not null"`
	Subdomain             string         `json:"subdomain" gorm:"column:subdomain;type:varchar(40);not null"`
	ContactEmail          *string        `json:"contact_email" gorm:"column:contact_email;type:varchar(255)"`
	ContactPhone          *string        `json:"contact_phone" gorm:"column:contact_phone;type:varchar(30)"`
	TaxCode               *string        `json:"tax_code" gorm:"column:tax_code;type:varchar(50)"`
	Address               *string        `json:"address" gorm:"column:address;type:text"`
	Status                string         `json:"status" gorm:"column:status;type:varchar(30);not null;default:'PENDING_APPROVAL'"`
	StatusReason          *string        `json:"status_reason" gorm:"column:status_reason;type:text"`
	ApprovedBy            *uuid.UUID     `json:"approved_by" gorm:"column:approved_by;type:uuid"`
	ApprovedAt            *time.Time     `json:"approved_at" gorm:"column:approved_at"`
	CloudflareDNSRecordID *string        `json:"cloudflare_dns_record_id" gorm:"column:cloudflare_dns_record_id;type:varchar(64)"`
	DNSStatus             string         `json:"dns_status" gorm:"column:dns_status;type:varchar(20);not null;default:'PENDING'"`
	DNSLastError          *string        `json:"dns_last_error" gorm:"column:dns_last_error;type:text"`
	DNSSyncedAt           *time.Time     `json:"dns_synced_at" gorm:"column:dns_synced_at"`
	CreatedAt             time.Time      `json:"created_at" gorm:"column:created_at"`
	UpdatedAt             time.Time      `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt             gorm.DeletedAt `json:"-" gorm:"column:deleted_at;index"`
}

func (Organization) TableName() string {
	return "organizations"
}

type School struct {
	ID             uuid.UUID      `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID uuid.UUID      `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	Code           string         `json:"code" gorm:"column:code;type:varchar(50);not null"`
	Name           string         `json:"name" gorm:"column:name;type:varchar(200);not null"`
	Level          *string        `json:"level" gorm:"column:level;type:varchar(50)"`
	Email          *string        `json:"email" gorm:"column:email;type:varchar(255)"`
	Phone          *string        `json:"phone" gorm:"column:phone;type:varchar(30)"`
	Address        *string        `json:"address" gorm:"column:address;type:text"`
	IsMain         bool           `json:"is_main" gorm:"column:is_main;not null;default:false"`
	Status         string         `json:"status" gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`
	CreatedAt      time.Time      `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time      `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt      gorm.DeletedAt `json:"-" gorm:"column:deleted_at;index"`
}

func (School) TableName() string {
	return "schools"
}

type TenantUser struct {
	ID                 uuid.UUID      `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Email              string         `json:"email" gorm:"column:email;type:varchar(255);not null"`
	PasswordHash       string         `json:"-" gorm:"column:password_hash;type:text;not null"`
	FullName           string         `json:"full_name" gorm:"column:full_name;type:varchar(150);not null"`
	Phone              *string        `json:"phone" gorm:"column:phone;type:varchar(20)"`
	Status             string         `json:"status" gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`
	MustChangePassword bool           `json:"must_change_password" gorm:"column:must_change_password;not null;default:false"`
	CreatedAt          time.Time      `json:"created_at" gorm:"column:created_at"`
	UpdatedAt          time.Time      `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt          gorm.DeletedAt `json:"-" gorm:"column:deleted_at;index"`
}

func (TenantUser) TableName() string {
	return "users"
}

type SchoolMembership struct {
	ID             uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID uuid.UUID `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	SchoolID       uuid.UUID `json:"school_id" gorm:"column:school_id;type:uuid;not null"`
	UserID         uuid.UUID `json:"user_id" gorm:"column:user_id;type:uuid;not null"`
	IsSchoolAdmin  bool      `json:"is_school_admin" gorm:"column:is_school_admin;not null;default:false"`
	Status         string    `json:"status" gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`
	CreatedAt      time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (SchoolMembership) TableName() string {
	return "school_memberships"
}

type ReservedSubdomain struct {
	Keyword   string     `json:"keyword" gorm:"column:keyword;type:varchar(40);primaryKey"`
	Reason    string     `json:"reason" gorm:"column:reason;type:varchar(255);not null"`
	CreatedBy *uuid.UUID `json:"created_by" gorm:"column:created_by;type:uuid"`
	CreatedAt time.Time  `json:"created_at" gorm:"column:created_at"`
}

func (ReservedSubdomain) TableName() string {
	return "reserved_subdomains"
}

type OrganizationSubdomainHistory struct {
	ID                    uuid.UUID  `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID        uuid.UUID  `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	OldSubdomain          string     `json:"old_subdomain" gorm:"column:old_subdomain;type:varchar(40);not null"`
	NewSubdomain          string     `json:"new_subdomain" gorm:"column:new_subdomain;type:varchar(40);not null"`
	ApprovedBy            uuid.UUID  `json:"approved_by" gorm:"column:approved_by;type:uuid;not null"`
	Reason                string     `json:"reason" gorm:"column:reason;type:text;not null"`
	RedirectUntil         *time.Time `json:"redirect_until" gorm:"column:redirect_until"`
	OldCloudflareRecordID *string    `json:"old_cloudflare_record_id" gorm:"column:old_cloudflare_record_id;type:varchar(64)"`
	OldRecordStatus       string     `json:"old_record_status" gorm:"column:old_record_status;type:varchar(20);not null;default:'REDIRECTING'"`
	CleanedUpAt           *time.Time `json:"cleaned_up_at" gorm:"column:cleaned_up_at"`
	CreatedAt             time.Time  `json:"created_at" gorm:"column:created_at"`
}

func (OrganizationSubdomainHistory) TableName() string {
	return "organization_subdomain_histories"
}

type OrganizationSummary struct {
	Organization
	SchoolCount        int64      `json:"school_count"`
	PrimaryPackageName *string    `json:"primary_package_name"`
	PrimaryExpiresAt   *time.Time `json:"primary_expires_at"`
}
