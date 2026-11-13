package iam

import (
	"context"
	"errors"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/audit"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrUserNotFound          = errors.New("platform user not found")
	ErrRoleNotFound          = errors.New("platform role not found")
	ErrEmailTaken            = errors.New("email already exists")
	ErrRoleCodeTaken         = errors.New("role code already exists")
	ErrLastRootAdmin         = errors.New("cannot remove or lock the last ROOT_ADMIN user")
	ErrSelfLockNotAllowed    = errors.New("cannot lock your own account")
	ErrWeakPassword          = errors.New("password must be at least 8 characters")
	ErrMFANotEnabled         = errors.New("target user does not have MFA enabled")
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

type ListUsersFilter struct {
	Search string
	Status string
	RoleID string
	Page   int
	Limit  int
}

func (s *Service) ListUsers(ctx context.Context, filter ListUsersFilter) ([]PlatformUser, int64, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 20
	}

	q := s.db.WithContext(ctx).Model(&PlatformUser{})
	if filter.Search != "" {
		like := "%" + strings.TrimSpace(filter.Search) + "%"
		q = q.Where("email ILIKE ? OR full_name ILIKE ?", like, like)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", strings.ToUpper(filter.Status))
	}
	if filter.RoleID != "" {
		q = q.Where("id IN (SELECT platform_user_id FROM platform_user_roles WHERE platform_role_id = ?)", filter.RoleID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var users []PlatformUser
	offset := (filter.Page - 1) * filter.Limit
	if err := q.Preload("Roles").Order("created_at DESC").Offset(offset).Limit(filter.Limit).Find(&users).Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

type CreateUserInput struct {
	Email      string      `json:"email"`
	Password   string      `json:"password"`
	FullName   string      `json:"full_name"`
	Phone      *string     `json:"phone"`
	RoleIDs    []uuid.UUID `json:"role_ids"`
	ActorID    uuid.UUID   `json:"-"`
	IPAddress  string      `json:"-"`
	UserAgent  string      `json:"-"`
}

func (s *Service) CreateUser(ctx context.Context, input CreateUserInput) (*PlatformUser, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	var existingCount int64
	s.db.WithContext(ctx).Model(&PlatformUser{}).Where("LOWER(email) = ?", email).Count(&existingCount)
	if existingCount > 0 {
		return nil, ErrEmailTaken
	}

	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &PlatformUser{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: string(hashedBytes),
		FullName:     strings.TrimSpace(input.FullName),
		Phone:        input.Phone,
		Status:       "ACTIVE",
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}

		for _, roleID := range input.RoleIDs {
			ur := PlatformUserRole{
				PlatformUserID: user.ID,
				PlatformRoleID: roleID,
				AssignedBy:     &input.ActorID,
			}
			if err := tx.Create(&ur).Error; err != nil {
				return err
			}
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "PLATFORM_USER_CREATED",
			ResourceType: "PLATFORM_USER",
			ResourceID:   &user.ID,
			AfterData: map[string]any{
				"id":        user.ID,
				"email":     user.Email,
				"full_name": user.FullName,
				"status":    user.Status,
				"role_ids":  input.RoleIDs,
			},
			IPAddress: input.IPAddress,
			UserAgent: input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	_ = s.db.WithContext(ctx).Preload("Roles").First(user, "id = ?", user.ID).Error
	return user, nil
}

// countActiveRootAdmins counts how many ACTIVE platform_users currently hold ROOT_ADMIN.
func (s *Service) countActiveRootAdmins(tx *gorm.DB) (int64, error) {
	var count int64
	err := tx.Raw(`
		SELECT COUNT(DISTINCT u.id)
		FROM platform_users u
		JOIN platform_user_roles ur ON ur.platform_user_id = u.id
		JOIN platform_roles r ON r.id = ur.platform_role_id
		WHERE u.deleted_at IS NULL
		  AND u.status = 'ACTIVE'
		  AND UPPER(r.code) = ?
	`, RootAdminRoleCode).Scan(&count).Error
	return count, err
}

func (s *Service) userHasRootAdmin(tx *gorm.DB, userID uuid.UUID) (bool, error) {
	var count int64
	err := tx.Raw(`
		SELECT COUNT(1)
		FROM platform_user_roles ur
		JOIN platform_roles r ON r.id = ur.platform_role_id
		WHERE ur.platform_user_id = ?
		  AND UPPER(r.code) = ?
	`, userID, RootAdminRoleCode).Scan(&count).Error
	return count > 0, err
}

func (s *Service) UpdateUserRoles(ctx context.Context, targetUserID uuid.UUID, roleIDs []uuid.UUID, actorID uuid.UUID, ip, ua string) error {
	var user PlatformUser
	if err := s.db.WithContext(ctx).Preload("Roles").First(&user, "id = ?", targetUserID).Error; err != nil {
		return ErrUserNotFound
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		hadRoot, err := s.userHasRootAdmin(tx, targetUserID)
		if err != nil {
			return err
		}

		var willHaveRoot bool
		if len(roleIDs) > 0 {
			var rootCount int64
			if err := tx.Model(&PlatformRole{}).
				Where("id IN ? AND UPPER(code) = ?", roleIDs, RootAdminRoleCode).
				Count(&rootCount).Error; err != nil {
				return err
			}
			willHaveRoot = rootCount > 0
		}

		if hadRoot && !willHaveRoot && user.Status == "ACTIVE" {
			activeRoots, err := s.countActiveRootAdmins(tx)
			if err != nil {
				return err
			}
			if activeRoots <= 1 {
				return ErrLastRootAdmin
			}
		}

		var oldRoleIDs []uuid.UUID
		for _, r := range user.Roles {
			oldRoleIDs = append(oldRoleIDs, r.ID)
		}

		if err := tx.Where("platform_user_id = ?", targetUserID).Delete(&PlatformUserRole{}).Error; err != nil {
			return err
		}

		for _, roleID := range roleIDs {
			ur := PlatformUserRole{
				PlatformUserID: targetUserID,
				PlatformRoleID: roleID,
				AssignedBy:     &actorID,
			}
			if err := tx.Create(&ur).Error; err != nil {
				return err
			}
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "PLATFORM_USER_ROLES_UPDATED",
			ResourceType: "PLATFORM_USER",
			ResourceID:   &targetUserID,
			BeforeData:   map[string]any{"role_ids": oldRoleIDs},
			AfterData:    map[string]any{"role_ids": roleIDs},
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
	if err != nil {
		return err
	}

	// Invalidate Redis authz cache
	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.PlatformUserAuthzKey(targetUserID.String())).Err()
	}

	return nil
}

func (s *Service) UpdateUserStatus(ctx context.Context, targetUserID uuid.UUID, newStatus, reason string, actorID uuid.UUID, ip, ua string) error {
	newStatus = strings.ToUpper(strings.TrimSpace(newStatus))
	if targetUserID == actorID && newStatus == "LOCKED" {
		return ErrSelfLockNotAllowed
	}

	var user PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", targetUserID).Error; err != nil {
		return ErrUserNotFound
	}

	oldStatus := user.Status

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if oldStatus == "ACTIVE" && newStatus != "ACTIVE" {
			hasRoot, err := s.userHasRootAdmin(tx, targetUserID)
			if err != nil {
				return err
			}
			if hasRoot {
				activeRoots, err := s.countActiveRootAdmins(tx)
				if err != nil {
					return err
				}
				if activeRoots <= 1 {
					return ErrLastRootAdmin
				}
			}
		}

		if err := tx.Model(&user).Updates(map[string]any{
			"status":     newStatus,
			"updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "PLATFORM_USER_STATUS_UPDATED",
			ResourceType: "PLATFORM_USER",
			ResourceID:   &targetUserID,
			BeforeData:   map[string]any{"status": oldStatus},
			AfterData:    map[string]any{"status": newStatus},
			Reason:       reason,
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
	if err != nil {
		return err
	}

	// Nếu LOCKED (hoặc INACTIVE), xóa toàn bộ session và authz cache của user trên Redis
	if s.rdb != nil {
		userIDStr := targetUserID.String()
		pipe := s.rdb.Pipeline()
		pipe.Del(ctx, rediskeys.PlatformUserAuthzKey(userIDStr))

		if newStatus == "LOCKED" || newStatus == "INACTIVE" {
			userSessionsKey := rediskeys.PlatformUserSessionsKey(userIDStr)
			jtis, err := s.rdb.SMembers(ctx, userSessionsKey).Result()
			if err == nil {
				for _, jti := range jtis {
					pipe.Del(ctx, rediskeys.PlatformSessionKey(jti))
					pipe.Set(ctx, rediskeys.PlatformBlacklistJTIKey(jti), "1", rediskeys.TTLPlatformSession)
				}
			}
			pipe.Del(ctx, userSessionsKey)
		}
		_, _ = pipe.Exec(ctx)
	}

	return nil
}

func (s *Service) ListRoles(ctx context.Context) ([]RoleWithSummary, error) {
	var roles []PlatformRole
	if err := s.db.WithContext(ctx).Preload("Permissions").Order("is_system DESC, created_at ASC").Find(&roles).Error; err != nil {
		return nil, err
	}

	type countRow struct {
		RoleID uuid.UUID
		Count  int64
	}
	var counts []countRow
	s.db.WithContext(ctx).Raw(`
		SELECT ur.platform_role_id AS role_id, COUNT(DISTINCT ur.platform_user_id) AS count
		FROM platform_user_roles ur
		JOIN platform_users u ON u.id = ur.platform_user_id AND u.deleted_at IS NULL
		GROUP BY ur.platform_role_id
	`).Scan(&counts)

	countMap := make(map[uuid.UUID]int64, len(counts))
	for _, c := range counts {
		countMap[c.RoleID] = c.Count
	}

	result := make([]RoleWithSummary, 0, len(roles))
	for _, r := range roles {
		permCodes := make([]string, 0, len(r.Permissions))
		for _, p := range r.Permissions {
			permCodes = append(permCodes, p.Code)
		}
		result = append(result, RoleWithSummary{
			PlatformRole:    r,
			UserCount:       countMap[r.ID],
			PermissionCodes: permCodes,
		})
	}
	return result, nil
}

func (s *Service) ListPermissions(ctx context.Context) ([]PlatformPermission, error) {
	var perms []PlatformPermission
	if err := s.db.WithContext(ctx).Order("module ASC, code ASC").Find(&perms).Error; err != nil {
		return nil, err
	}
	return perms, nil
}

type CreateRoleInput struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	ActorID     uuid.UUID `json:"-"`
	IPAddress   string    `json:"-"`
	UserAgent   string    `json:"-"`
}

func (s *Service) CreateRole(ctx context.Context, input CreateRoleInput) (*PlatformRole, error) {
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	var existing int64
	s.db.WithContext(ctx).Model(&PlatformRole{}).Where("UPPER(code) = ?", code).Count(&existing)
	if existing > 0 {
		return nil, ErrRoleCodeTaken
	}

	role := &PlatformRole{
		ID:          uuid.New(),
		Code:        code,
		Name:        strings.TrimSpace(input.Name),
		Description: input.Description,
		IsSystem:    false,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(role).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "PLATFORM_ROLE_CREATED",
			ResourceType: "PLATFORM_ROLE",
			ResourceID:   &role.ID,
			AfterData:    role,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}
	return role, nil
}

func (s *Service) UpdateRolePermissions(ctx context.Context, roleID uuid.UUID, permissionIDs []uuid.UUID, permissionCodes []string, actorID uuid.UUID, ip, ua string) error {
	var role PlatformRole
	if err := s.db.WithContext(ctx).Preload("Permissions").First(&role, "id = ?", roleID).Error; err != nil {
		return ErrRoleNotFound
	}

	// Resolve permissionCodes to IDs if provided
	resolvedIDs := make([]uuid.UUID, 0, len(permissionIDs)+len(permissionCodes))
	seen := make(map[uuid.UUID]bool)
	for _, id := range permissionIDs {
		if !seen[id] {
			seen[id] = true
			resolvedIDs = append(resolvedIDs, id)
		}
	}
	if len(permissionCodes) > 0 {
		var perms []PlatformPermission
		if err := s.db.WithContext(ctx).Where("code IN ?", permissionCodes).Find(&perms).Error; err != nil {
			return err
		}
		for _, p := range perms {
			if !seen[p.ID] {
				seen[p.ID] = true
				resolvedIDs = append(resolvedIDs, p.ID)
			}
		}
	}

	var affectedUserIDs []string

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("platform_role_id = ?", roleID).Delete(&PlatformRolePermission{}).Error; err != nil {
			return err
		}

		for _, pid := range resolvedIDs {
			rp := PlatformRolePermission{
				PlatformRoleID:       roleID,
				PlatformPermissionID: pid,
			}
			if err := tx.Create(&rp).Error; err != nil {
				return err
			}
		}

		if err := tx.Raw(`
			SELECT DISTINCT platform_user_id::text
			FROM platform_user_roles
			WHERE platform_role_id = ?
		`, roleID).Scan(&affectedUserIDs).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "PLATFORM_ROLE_PERMISSIONS_UPDATED",
			ResourceType: "PLATFORM_ROLE",
			ResourceID:   &roleID,
			AfterData: map[string]any{
				"role_id":        roleID,
				"permission_ids": resolvedIDs,
			},
			IPAddress: ip,
			UserAgent: ua,
		})
	})
	if err != nil {
		return err
	}

	// Xóa cache platform:authz:user:* của tất cả user thuộc role này bằng Pipeline
	if s.rdb != nil && len(affectedUserIDs) > 0 {
		pipe := s.rdb.Pipeline()
		for _, uid := range affectedUserIDs {
			pipe.Del(ctx, rediskeys.PlatformUserAuthzKey(uid))
		}
		_, _ = pipe.Exec(ctx)
	}

	return nil
}

// ResetUserPassword allows a Platform Admin to reset a member's password and revoke their active sessions.
func (s *Service) ResetUserPassword(ctx context.Context, targetUserID uuid.UUID, newPassword string, mustChange bool, reason string, actorID uuid.UUID, ip, ua string) error {
	if len(strings.TrimSpace(newPassword)) < 8 {
		return ErrWeakPassword
	}

	var user PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", targetUserID).Error; err != nil {
		return ErrUserNotFound
	}

	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	now := time.Now()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user).Updates(map[string]any{
			"password_hash":        string(hashedBytes),
			"must_change_password": mustChange,
			"updated_at":           now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "PLATFORM_USER_PASSWORD_RESET",
			ResourceType: "PLATFORM_USER",
			ResourceID:   &targetUserID,
			AfterData: map[string]any{
				"user_id":              targetUserID,
				"must_change_password": mustChange,
			},
			Reason:    reason,
			IPAddress: ip,
			UserAgent: ua,
		})
	})
	if err != nil {
		return err
	}

	s.revokeAllUserSessions(ctx, targetUserID.String())
	return nil
}

// ForceDisableUserMFA allows a Platform Admin to turn off MFA for a member who lost their authenticator device.
func (s *Service) ForceDisableUserMFA(ctx context.Context, targetUserID uuid.UUID, reason string, actorID uuid.UUID, ip, ua string) error {
	var user PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", targetUserID).Error; err != nil {
		return ErrUserNotFound
	}
	if !user.MFAEnabled {
		return ErrMFANotEnabled
	}

	now := time.Now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user).Updates(map[string]any{
			"mfa_enabled": false,
			"mfa_secret":  nil,
			"updated_at":  now,
		}).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "PLATFORM_USER_MFA_FORCE_DISABLED",
			ResourceType: "PLATFORM_USER",
			ResourceID:   &targetUserID,
			BeforeData:   map[string]any{"mfa_enabled": true},
			AfterData:    map[string]any{"mfa_enabled": false},
			Reason:       reason,
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
	if err != nil {
		return err
	}

	// Vô hiệu hóa toàn bộ session cũ để đảm bảo thiết bị bị mất không còn truy cập được
	s.revokeAllUserSessions(ctx, targetUserID.String())
	return nil
}

func (s *Service) revokeAllUserSessions(ctx context.Context, userIDStr string) {
	if s.rdb == nil {
		return
	}
	userSessionsKey := rediskeys.PlatformUserSessionsKey(userIDStr)
	jtis, err := s.rdb.SMembers(ctx, userSessionsKey).Result()
	pipe := s.rdb.Pipeline()
	if err == nil {
		for _, jti := range jtis {
			pipe.Del(ctx, rediskeys.PlatformSessionKey(jti))
			pipe.Set(ctx, rediskeys.PlatformBlacklistJTIKey(jti), "1", rediskeys.TTLPlatformSession)
		}
	}
	pipe.Del(ctx, userSessionsKey)
	_, _ = pipe.Exec(ctx)
}

