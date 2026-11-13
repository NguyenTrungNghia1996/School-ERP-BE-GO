package billing

import (
	"encoding/json"
	"school-erp/internal/platform/catalog"
	"time"

	"github.com/google/uuid"
)

type ServicePackage struct {
	ID          uuid.UUID               `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Code        string                  `json:"code" gorm:"column:code;type:varchar(50);not null"`
	Name        string                  `json:"name" gorm:"column:name;type:varchar(150);not null"`
	Description *string                 `json:"description" gorm:"column:description;type:text"`
	PackageType string                  `json:"package_type" gorm:"column:package_type;type:varchar(20);not null"` // PRIMARY | ADDON
	IsActive    bool                    `json:"is_active" gorm:"column:is_active;not null;default:true"`
	CreatedAt   time.Time               `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time               `json:"updated_at" gorm:"column:updated_at"`
	Versions    []ServicePackageVersion `json:"versions,omitempty" gorm:"foreignKey:PackageID"`
}

func (ServicePackage) TableName() string {
	return "service_packages"
}

type ServicePackageVersion struct {
	ID          uuid.UUID         `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	PackageID   uuid.UUID         `json:"package_id" gorm:"column:package_id;type:uuid;not null"`
	Version     int               `json:"version" gorm:"column:version;not null"`
	Status      string            `json:"status" gorm:"column:status;type:varchar(20);not null;default:'DRAFT'"` // DRAFT | PUBLISHED | ARCHIVED
	Quotas      json.RawMessage   `json:"quotas" gorm:"column:quotas;type:jsonb;not null;default:'{}'"`
	Notes       *string           `json:"notes" gorm:"column:notes;type:text"`
	PublishedAt *time.Time        `json:"published_at" gorm:"column:published_at"`
	CreatedBy   *uuid.UUID        `json:"created_by" gorm:"column:created_by;type:uuid"`
	CreatedAt   time.Time         `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time         `json:"updated_at" gorm:"column:updated_at"`
	Features    []catalog.Feature `json:"features,omitempty" gorm:"many2many:service_package_features;joinForeignKey:PackageVersionID;JoinReferences:FeatureID"`
}

func (ServicePackageVersion) TableName() string {
	return "service_package_versions"
}

type ServicePackageFeature struct {
	PackageVersionID uuid.UUID `json:"package_version_id" gorm:"column:package_version_id;type:uuid;primaryKey"`
	FeatureID        uuid.UUID `json:"feature_id" gorm:"column:feature_id;type:uuid;primaryKey"`
	CreatedAt        time.Time `json:"created_at" gorm:"column:created_at"`
}

func (ServicePackageFeature) TableName() string {
	return "service_package_features"
}

type OrganizationSubscription struct {
	ID               uuid.UUID  `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID   uuid.UUID  `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	PackageID        uuid.UUID  `json:"package_id" gorm:"column:package_id;type:uuid;not null"`
	PackageVersionID uuid.UUID  `json:"package_version_id" gorm:"column:package_version_id;type:uuid;not null"`
	SubscriptionType string     `json:"subscription_type" gorm:"column:subscription_type;type:varchar(20);not null"` // PRIMARY | ADDON
	Status           string     `json:"status" gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`
	StartedAt        time.Time  `json:"started_at" gorm:"column:started_at;not null"`
	ExpiresAt        time.Time  `json:"expires_at" gorm:"column:expires_at;not null"`
	RevokedAt        *time.Time `json:"revoked_at" gorm:"column:revoked_at"`
	RevokedBy        *uuid.UUID `json:"revoked_by" gorm:"column:revoked_by;type:uuid"`
	RevokeReason     *string    `json:"revoke_reason" gorm:"column:revoke_reason;type:text"`
	CreatedBy        *uuid.UUID `json:"created_by" gorm:"column:created_by;type:uuid"`
	CreatedAt        time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (OrganizationSubscription) TableName() string {
	return "organization_subscriptions"
}
