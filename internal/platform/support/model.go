package support

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type SupportAccessSession struct {
	ID                  uuid.UUID       `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ActorPlatformUserID uuid.UUID       `json:"actor_platform_user_id" gorm:"column:actor_platform_user_id;type:uuid;not null"`
	OrganizationID      uuid.UUID       `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	SchoolID            *uuid.UUID      `json:"school_id" gorm:"column:school_id;type:uuid"`
	SessionType         string          `json:"session_type" gorm:"column:session_type;type:varchar(20);not null"` // READ_ONLY | READ_WRITE
	Reason              string          `json:"reason" gorm:"column:reason;type:text;not null"`
	GrantedPermissions  json.RawMessage `json:"granted_permissions" gorm:"column:granted_permissions;type:jsonb;not null;default:'[]'" swaggertype:"array,string"`
	Status              string          `json:"status" gorm:"column:status;type:varchar(20);not null;default:'PENDING'"` // PENDING | APPROVED | ACTIVE | EXPIRED | REVOKED | REJECTED
	ApprovedBy          *uuid.UUID      `json:"approved_by" gorm:"column:approved_by;type:uuid"`
	StartedAt           *time.Time      `json:"started_at" gorm:"column:started_at"`
	ExpiresAt           time.Time       `json:"expires_at" gorm:"column:expires_at;not null"`
	RevokedAt           *time.Time      `json:"revoked_at" gorm:"column:revoked_at"`
	RevokedBy           *uuid.UUID      `json:"revoked_by" gorm:"column:revoked_by;type:uuid"`
	RevokeReason        *string         `json:"revoke_reason" gorm:"column:revoke_reason;type:text"`
	CreatedAt           time.Time       `json:"created_at" gorm:"column:created_at"`
}

func (SupportAccessSession) TableName() string {
	return "support_access_sessions"
}
