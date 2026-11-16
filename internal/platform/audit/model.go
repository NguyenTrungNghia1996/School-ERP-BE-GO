package audit

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type AuditLog struct {
	ID               uuid.UUID       `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ActorType        string          `json:"actor_type" gorm:"column:actor_type;type:varchar(20);not null"`
	ActorID          *uuid.UUID      `json:"actor_id" gorm:"column:actor_id;type:uuid"`
	SupportSessionID *uuid.UUID      `json:"support_session_id" gorm:"column:support_session_id;type:uuid"`
	OrganizationID   *uuid.UUID      `json:"organization_id" gorm:"column:organization_id;type:uuid"`
	SchoolID         *uuid.UUID      `json:"school_id" gorm:"column:school_id;type:uuid"`
	Action           string          `json:"action" gorm:"column:action;type:varchar(100);not null"`
	ResourceType     string          `json:"resource_type" gorm:"column:resource_type;type:varchar(60);not null"`
	ResourceID       *uuid.UUID      `json:"resource_id" gorm:"column:resource_id;type:uuid"`
	BeforeData       json.RawMessage `json:"before_data" gorm:"column:before_data;type:jsonb" swaggertype:"object"`
	AfterData        json.RawMessage `json:"after_data" gorm:"column:after_data;type:jsonb" swaggertype:"object"`
	Reason           *string         `json:"reason" gorm:"column:reason;type:text"`
	IPAddress        *string         `json:"ip_address" gorm:"column:ip_address;type:inet"`
	UserAgent        *string         `json:"user_agent" gorm:"column:user_agent;type:text"`
	CreatedAt        time.Time       `json:"created_at" gorm:"column:created_at;autoCreateTime"`
}

func (AuditLog) TableName() string {
	return "audit_logs"
}

type AuditEntry struct {
	ActorType        string
	ActorID          *uuid.UUID
	SupportSessionID *uuid.UUID
	OrganizationID   *uuid.UUID
	SchoolID         *uuid.UUID
	Action           string
	ResourceType     string
	ResourceID       *uuid.UUID
	BeforeData       any
	AfterData        any
	Reason           string
	IPAddress        string
	UserAgent        string
}
