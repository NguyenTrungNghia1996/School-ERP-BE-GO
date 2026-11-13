package audit

import (
	"encoding/json"
	"net"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

var sensitiveKeys = map[string]bool{
	"password":      true,
	"password_hash": true,
	"mfa_secret":    true,
	"refresh_token": true,
	"access_token":  true,
}

func sanitizeJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		sanitizeMap(m)
		cleaned, err := json.Marshal(m)
		if err == nil {
			return cleaned
		}
	}
	return raw
}

func sanitizeMap(m map[string]any) {
	for k, val := range m {
		if sensitiveKeys[strings.ToLower(k)] {
			delete(m, k)
			continue
		}
		if sub, ok := val.(map[string]any); ok {
			sanitizeMap(sub)
		}
	}
}

// Log writes an audit log entry inside the given GORM transaction (or default DB if tx is nil).
func (s *Service) Log(tx *gorm.DB, entry AuditEntry) error {
	db := tx
	if db == nil {
		db = s.db
	}

	actorType := entry.ActorType
	if actorType == "" {
		actorType = "PLATFORM"
	}

	var ipPtr *string
	if entry.IPAddress != "" {
		if parsed := net.ParseIP(entry.IPAddress); parsed != nil {
			ipStr := parsed.String()
			ipPtr = &ipStr
		}
	}

	var uaPtr *string
	if entry.UserAgent != "" {
		ua := entry.UserAgent
		uaPtr = &ua
	}

	var reasonPtr *string
	if entry.Reason != "" {
		r := entry.Reason
		reasonPtr = &r
	}

	record := AuditLog{
		ID:               uuid.New(),
		ActorType:        actorType,
		ActorID:          entry.ActorID,
		SupportSessionID: entry.SupportSessionID,
		OrganizationID:   entry.OrganizationID,
		SchoolID:         entry.SchoolID,
		Action:           entry.Action,
		ResourceType:     entry.ResourceType,
		ResourceID:       entry.ResourceID,
		BeforeData:       sanitizeJSON(entry.BeforeData),
		AfterData:        sanitizeJSON(entry.AfterData),
		Reason:           reasonPtr,
		IPAddress:        ipPtr,
		UserAgent:        uaPtr,
	}

	return db.Create(&record).Error
}

type ListFilter struct {
	ActorType      string
	ActorID        string
	OrganizationID string
	Action         string
	ResourceType   string
	Page           int
	Limit          int
}

func (s *Service) List(filter ListFilter) ([]AuditLog, int64, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 20
	}

	q := s.db.Model(&AuditLog{})
	if filter.ActorType != "" {
		q = q.Where("actor_type = ?", filter.ActorType)
	}
	if filter.ActorID != "" {
		q = q.Where("actor_id = ?", filter.ActorID)
	}
	if filter.OrganizationID != "" {
		q = q.Where("organization_id = ?", filter.OrganizationID)
	}
	if filter.Action != "" {
		q = q.Where("action ILIKE ?", "%"+filter.Action+"%")
	}
	if filter.ResourceType != "" {
		q = q.Where("resource_type = ?", filter.ResourceType)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []AuditLog
	offset := (filter.Page - 1) * filter.Limit
	if err := q.Order("created_at DESC").Offset(offset).Limit(filter.Limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}

	return logs, total, nil
}
