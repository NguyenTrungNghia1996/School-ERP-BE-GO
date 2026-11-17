package media

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type OrganizationStorageUsage struct {
	OrganizationID uuid.UUID `json:"organization_id" gorm:"column:organization_id;type:uuid;primaryKey"`
	UsedBytes      int64     `json:"used_bytes" gorm:"column:used_bytes;not null;default:0"`
	FileCount      int64     `json:"file_count" gorm:"column:file_count;not null;default:0"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (OrganizationStorageUsage) TableName() string {
	return "organization_storage_usages"
}

type StoredFile struct {
	ID              uuid.UUID      `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID  uuid.UUID      `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	SchoolID        *uuid.UUID     `json:"school_id,omitempty" gorm:"column:school_id;type:uuid"`
	UploadedBy      *uuid.UUID     `json:"uploaded_by,omitempty" gorm:"column:uploaded_by;type:uuid"`
	Folder          string         `json:"folder" gorm:"column:folder;type:varchar(100);not null;default:'general'"`
	ObjectKey       string         `json:"object_key" gorm:"column:object_key;type:text;not null;unique"`
	OriginalName    string         `json:"original_name" gorm:"column:original_name;type:varchar(255);not null"`
	MimeType        string         `json:"mime_type" gorm:"column:mime_type;type:varchar(100);not null"`
	SizeBytes       int64          `json:"size_bytes" gorm:"column:size_bytes;not null"`
	StorageProvider string         `json:"storage_provider" gorm:"column:storage_provider;type:varchar(30);not null;default:'R2'"`
	Bucket          string         `json:"bucket" gorm:"column:bucket;type:varchar(120);not null"`
	PublicURL       *string        `json:"public_url,omitempty" gorm:"column:public_url;type:text"`
	AccessURL       string         `json:"access_url,omitempty" gorm:"-"`
	CreatedAt       time.Time      `json:"created_at" gorm:"column:created_at"`
	DeletedAt       gorm.DeletedAt `json:"-" gorm:"column:deleted_at;index"`
}

func (StoredFile) TableName() string {
	return "stored_files"
}

type StorageUsageOutput struct {
	OrganizationID uuid.UUID `json:"organization_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	FolderPrefix   string    `json:"folder_prefix" example:"orgs/550e8400-e29b-41d4-a716-446655440000/"`
	UsedBytes      int64     `json:"used_bytes" example:"1048576"`
	QuotaBytes     int64     `json:"quota_bytes" example:"53687091200"`
	RemainingBytes int64     `json:"remaining_bytes" example:"53686042624"`
	UsagePercent   float64   `json:"usage_percent" example:"0.01"`
	FileCount      int64     `json:"file_count" example:"3"`
}

type MediaFileURLOutput struct {
	FileID    uuid.UUID `json:"file_id"`
	ObjectKey string    `json:"object_key"`
	URL       string    `json:"url"`
	ExpiresIn int       `json:"expires_in_seconds,omitempty"`
}
