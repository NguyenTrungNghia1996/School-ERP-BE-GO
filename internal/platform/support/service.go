package support

import (
	"context"
	"encoding/json"
	"errors"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/audit"
	"school-erp/internal/platform/auth"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrSessionNotFound        = errors.New("support session not found")
	ErrSelfApprovalNotAllowed = errors.New("cannot approve your own support session (four-eyes principle)")
	ErrSessionNotPending      = errors.New("support session is not in PENDING status")
	ErrSessionNotApproved     = errors.New("support session must be APPROVED before token exchange")
	ErrSessionExpired         = errors.New("support session has already expired")
	ErrNotSessionOwner        = errors.New("only the session requester can exchange the support token")
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

type ListFilter struct {
	Status         string
	OrganizationID string
	Page           int
	Limit          int
}

func (s *Service) ListSessions(ctx context.Context, filter ListFilter) ([]SupportAccessSession, int64, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 20
	}

	q := s.db.WithContext(ctx).Model(&SupportAccessSession{})
	if filter.Status != "" {
		q = q.Where("status = ?", strings.ToUpper(filter.Status))
	}
	if filter.OrganizationID != "" {
		q = q.Where("organization_id = ?", filter.OrganizationID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var sessions []SupportAccessSession
	offset := (filter.Page - 1) * filter.Limit
	if err := q.Order("created_at DESC").Offset(offset).Limit(filter.Limit).Find(&sessions).Error; err != nil {
		return nil, 0, err
	}

	return sessions, total, nil
}

type CreateSessionInput struct {
	OrganizationID     uuid.UUID  `json:"organization_id"`
	SchoolID           *uuid.UUID `json:"school_id"`
	SessionType        string     `json:"session_type"`
	Reason             string     `json:"reason"`
	DurationMinutes    int        `json:"duration_minutes"`
	GrantedPermissions []string   `json:"granted_permissions"`
	ActorID            uuid.UUID  `json:"-"`
	IPAddress          string     `json:"-"`
	UserAgent          string     `json:"-"`
}

func (s *Service) CreateSession(ctx context.Context, input CreateSessionInput) (*SupportAccessSession, error) {
	sessionType := strings.ToUpper(strings.TrimSpace(input.SessionType))
	if input.DurationMinutes <= 0 {
		input.DurationMinutes = 60
	}
	if input.DurationMinutes > 480 {
		input.DurationMinutes = 480
	}

	permsJSON := []byte("[]")
	if len(input.GrantedPermissions) > 0 {
		if b, err := json.Marshal(input.GrantedPermissions); err == nil {
			permsJSON = b
		}
	}

	status := "PENDING"
	var approvedBy *uuid.UUID
	if sessionType == "READ_ONLY" {
		status = "APPROVED"
		approvedBy = &input.ActorID
	}

	expiresAt := time.Now().Add(time.Duration(input.DurationMinutes) * time.Minute)

	session := &SupportAccessSession{
		ID:                  uuid.New(),
		ActorPlatformUserID: input.ActorID,
		OrganizationID:      input.OrganizationID,
		SchoolID:            input.SchoolID,
		SessionType:         sessionType,
		Reason:              strings.TrimSpace(input.Reason),
		GrantedPermissions:  permsJSON,
		Status:              status,
		ApprovedBy:          approvedBy,
		ExpiresAt:           expiresAt,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(session).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:        "PLATFORM",
			ActorID:          &input.ActorID,
			SupportSessionID: &session.ID,
			OrganizationID:   &input.OrganizationID,
			SchoolID:         input.SchoolID,
			Action:           "SUPPORT_SESSION_REQUESTED",
			ResourceType:     "SUPPORT_ACCESS_SESSION",
			ResourceID:       &session.ID,
			AfterData:        session,
			Reason:           session.Reason,
			IPAddress:        input.IPAddress,
			UserAgent:        input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	return session, nil
}

func (s *Service) ApproveSession(ctx context.Context, sessionID, approverID uuid.UUID, ip, ua string) (*SupportAccessSession, error) {
	var session SupportAccessSession
	if err := s.db.WithContext(ctx).First(&session, "id = ?", sessionID).Error; err != nil {
		return nil, ErrSessionNotFound
	}

	// Four-Eyes Guard
	if session.ActorPlatformUserID == approverID {
		return nil, ErrSelfApprovalNotAllowed
	}

	if session.Status != "PENDING" {
		return nil, ErrSessionNotPending
	}

	oldStatus := session.Status

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&session).Updates(map[string]any{
			"status":      "APPROVED",
			"approved_by": approverID,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:        "PLATFORM",
			ActorID:          &approverID,
			SupportSessionID: &session.ID,
			OrganizationID:   &session.OrganizationID,
			SchoolID:         session.SchoolID,
			Action:           "SUPPORT_SESSION_APPROVED",
			ResourceType:     "SUPPORT_ACCESS_SESSION",
			ResourceID:       &session.ID,
			BeforeData:       map[string]any{"status": oldStatus},
			AfterData:        map[string]any{"status": "APPROVED", "approved_by": approverID},
			IPAddress:        ip,
			UserAgent:        ua,
		})
	})
	if err != nil {
		return nil, err
	}

	session.Status = "APPROVED"
	session.ApprovedBy = &approverID
	return &session, nil
}

func (s *Service) ExchangeToken(ctx context.Context, sessionID, actorID uuid.UUID, ip, ua string) (map[string]any, error) {
	var session SupportAccessSession
	if err := s.db.WithContext(ctx).First(&session, "id = ?", sessionID).Error; err != nil {
		return nil, ErrSessionNotFound
	}

	if session.ActorPlatformUserID != actorID {
		return nil, ErrNotSessionOwner
	}

	if session.Status != "APPROVED" && session.Status != "ACTIVE" {
		return nil, ErrSessionNotApproved
	}

	now := time.Now()
	if !session.ExpiresAt.After(now) {
		return nil, ErrSessionExpired
	}

	schoolIDStr := ""
	if session.SchoolID != nil {
		schoolIDStr = session.SchoolID.String()
	}

	tokenStr, err := auth.GenerateSupportToken(
		actorID.String(),
		session.ID.String(),
		session.OrganizationID.String(),
		schoolIDStr,
		session.SessionType,
		session.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&session).Updates(map[string]any{
			"status":     "ACTIVE",
			"started_at": now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:        "PLATFORM",
			ActorID:          &actorID,
			SupportSessionID: &session.ID,
			OrganizationID:   &session.OrganizationID,
			SchoolID:         session.SchoolID,
			Action:           "SUPPORT_SESSION_TOKEN_EXCHANGED",
			ResourceType:     "SUPPORT_ACCESS_SESSION",
			ResourceID:       &session.ID,
			AfterData:        map[string]any{"status": "ACTIVE", "started_at": now, "expires_at": session.ExpiresAt},
			IPAddress:        ip,
			UserAgent:        ua,
		})
	})
	if err != nil {
		return nil, err
	}

	ttl := time.Until(session.ExpiresAt)
	if s.rdb != nil && ttl > 0 {
		payload, _ := json.Marshal(map[string]any{
			"session_id":      session.ID,
			"actor_id":        session.ActorPlatformUserID,
			"organization_id": session.OrganizationID,
			"school_id":       session.SchoolID,
			"session_type":    session.SessionType,
			"expires_at":      session.ExpiresAt,
		})
		_ = s.rdb.Set(ctx, rediskeys.SupportSessionKey(session.ID.String()), string(payload), ttl).Err()
	}

	return map[string]any{
		"support_token": tokenStr,
		"session_id":    session.ID,
		"session_type":  session.SessionType,
		"expires_at":    session.ExpiresAt,
		"ttl_seconds":   int(ttl.Seconds()),
	}, nil
}

func (s *Service) RevokeSession(ctx context.Context, sessionID uuid.UUID, reason string, actorID uuid.UUID, ip, ua string) (*SupportAccessSession, error) {
	var session SupportAccessSession
	if err := s.db.WithContext(ctx).First(&session, "id = ?", sessionID).Error; err != nil {
		return nil, ErrSessionNotFound
	}

	oldStatus := session.Status
	now := time.Now()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&session).Updates(map[string]any{
			"status":        "REVOKED",
			"revoked_at":    now,
			"revoked_by":    actorID,
			"revoke_reason": reason,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:        "PLATFORM",
			ActorID:          &actorID,
			SupportSessionID: &session.ID,
			OrganizationID:   &session.OrganizationID,
			SchoolID:         session.SchoolID,
			Action:           "SUPPORT_SESSION_REVOKED",
			ResourceType:     "SUPPORT_ACCESS_SESSION",
			ResourceID:       &session.ID,
			BeforeData:       map[string]any{"status": oldStatus},
			AfterData:        map[string]any{"status": "REVOKED", "revoked_at": now},
			Reason:           reason,
			IPAddress:        ip,
			UserAgent:        ua,
		})
	})
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.SupportSessionKey(session.ID.String())).Err()
	}

	session.Status = "REVOKED"
	session.RevokedAt = &now
	session.RevokedBy = &actorID
	session.RevokeReason = &reason
	return &session, nil
}
