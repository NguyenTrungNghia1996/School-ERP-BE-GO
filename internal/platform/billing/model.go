package billing

import (
	"encoding/json"
	"school-erp/internal/platform/catalog"
	"time"

	"github.com/google/uuid"
)

type ServicePackage struct {
	ID          uuid.UUID               `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Code        string                  `json:"code" gorm:"column:code;type:varchar(60);not null"`
	Name        string                  `json:"name" gorm:"column:name;type:varchar(150);not null"`
	Description *string                 `json:"description" gorm:"column:description;type:text"`
	PackageType string                  `json:"package_type" gorm:"column:package_type;type:varchar(20);not null"` // PRIMARY | ADDON
	IsPublic    bool                    `json:"is_public" gorm:"column:is_public;not null;default:true"`
	IsActive    bool                    `json:"is_active" gorm:"column:is_active;not null;default:true"`
	CreatedBy   *uuid.UUID              `json:"created_by" gorm:"column:created_by;type:uuid"`
	CreatedAt   time.Time               `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time               `json:"updated_at" gorm:"column:updated_at"`
	Versions    []ServicePackageVersion `json:"versions,omitempty" gorm:"foreignKey:PackageID"`
}

func (ServicePackage) TableName() string {
	return "service_packages"
}

type ServicePackageWithSummary struct {
	ServicePackage
	LatestPublishedVersion *ServicePackageVersion `json:"latest_published_version"`
	ActiveOrgCount         int64                  `json:"active_org_count"`
}

type ServicePackageVersion struct {
	ID            uuid.UUID         `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	PackageID     uuid.UUID         `json:"package_id" gorm:"column:package_id;type:uuid;not null"`
	Package       *ServicePackage   `json:"package,omitempty" gorm:"foreignKey:PackageID"`
	VersionNumber int               `json:"version_number" gorm:"column:version_number;not null"`
	VersionLabel  string            `json:"version_label" gorm:"column:version_label;type:varchar(40);not null"`
	Status        string            `json:"status" gorm:"column:status;type:varchar(20);not null;default:'DRAFT'"` // DRAFT | PUBLISHED | DEPRECATED
	BillingCycle  string            `json:"billing_cycle" gorm:"column:billing_cycle;type:varchar(20);not null;default:'YEARLY'"`
	BasePrice     float64           `json:"base_price" gorm:"column:base_price;type:numeric(15,2);not null;default:0"`
	Currency      string            `json:"currency" gorm:"column:currency;type:varchar(10);not null;default:'VND'"`
	Quotas        json.RawMessage   `json:"quotas" gorm:"column:quotas;type:jsonb;not null;default:'{}'" swaggertype:"object"`
	ReleaseNotes  *string           `json:"release_notes" gorm:"column:release_notes;type:text"`
	PublishedAt   *time.Time        `json:"published_at" gorm:"column:published_at"`
	PublishedBy   *uuid.UUID        `json:"published_by" gorm:"column:published_by;type:uuid"`
	CreatedAt     time.Time         `json:"created_at" gorm:"column:created_at"`
	UpdatedAt     time.Time         `json:"updated_at" gorm:"column:updated_at"`
	Features      []catalog.Feature `json:"features,omitempty" gorm:"many2many:service_package_features;joinForeignKey:PackageVersionID;JoinReferences:FeatureID"`
}

func (ServicePackageVersion) TableName() string {
	return "service_package_versions"
}

type ServicePackageFeature struct {
	PackageVersionID uuid.UUID       `json:"package_version_id" gorm:"column:package_version_id;type:uuid;primaryKey"`
	FeatureID        uuid.UUID       `json:"feature_id" gorm:"column:feature_id;type:uuid;primaryKey"`
	FeatureLimits    json.RawMessage `json:"feature_limits" gorm:"column:feature_limits;type:jsonb;not null;default:'{}'" swaggertype:"object"`
	CreatedAt        time.Time       `json:"created_at" gorm:"column:created_at"`
}

func (ServicePackageFeature) TableName() string {
	return "service_package_features"
}

type OrganizationSubscription struct {
	ID               uuid.UUID              `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID   uuid.UUID              `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	PackageVersionID uuid.UUID              `json:"package_version_id" gorm:"column:package_version_id;type:uuid;not null"`
	PackageVersion   *ServicePackageVersion `json:"package_version,omitempty" gorm:"foreignKey:PackageVersionID"`
	SubscriptionType string                 `json:"subscription_type" gorm:"column:subscription_type;type:varchar(20);not null"` // PRIMARY | ADDON
	Status           string                 `json:"status" gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`    // SCHEDULED | ACTIVE | SUSPENDED | EXPIRED | REVOKED
	StartedAt        time.Time              `json:"started_at" gorm:"column:started_at;not null"`
	ExpiresAt        time.Time              `json:"expires_at" gorm:"column:expires_at;not null"`
	CustomQuotas     json.RawMessage        `json:"custom_quotas" gorm:"column:custom_quotas;type:jsonb" swaggertype:"object"`
	AutoRenew        bool                   `json:"auto_renew" gorm:"column:auto_renew;not null;default:false"`
	Notes            *string                `json:"notes" gorm:"column:notes;type:text"`
	AssignedBy       *uuid.UUID             `json:"assigned_by" gorm:"column:assigned_by;type:uuid"`
	RevokedAt        *time.Time             `json:"revoked_at" gorm:"column:revoked_at"`
	RevokedBy        *uuid.UUID             `json:"revoked_by" gorm:"column:revoked_by;type:uuid"`
	RevokeReason     *string                `json:"revoke_reason" gorm:"column:revoke_reason;type:text"`
	CreatedAt        time.Time              `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time              `json:"updated_at" gorm:"column:updated_at"`
}

func (OrganizationSubscription) TableName() string {
	return "organization_subscriptions"
}

type ActivePackageSummary struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	PackageCode    string    `json:"package_code"`
	PackageName    string    `json:"package_name"`
	VersionNumber  int       `json:"version_number"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type OrganizationEntitlement struct {
	OrganizationID  string                 `json:"organization_id"`
	PrimaryPackage  *ActivePackageSummary  `json:"primary_package"`
	ActiveAddons    []ActivePackageSummary `json:"active_addons"`
	EnabledFeatures []string               `json:"enabled_features"`
	EffectiveQuotas map[string]int64       `json:"effective_quotas"`
	CalculatedAt    time.Time              `json:"calculated_at"`
	NearestExpiryAt *time.Time             `json:"nearest_expiry_at"`
}
