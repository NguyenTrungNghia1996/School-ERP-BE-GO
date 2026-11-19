package iam

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
	ErrUserNotFound             = errors.New("platform user not found")
	ErrRoleNotFound             = errors.New("platform role not found")
	ErrEmailTaken               = errors.New("email already exists")
	ErrRoleCodeTaken            = errors.New("role code already exists")
	ErrInvalidRoleCodeFormat    = errors.New("role code must match ^[A-Z0-9_]+$")
	ErrInvalidStatus            = errors.New("invalid user status")
	ErrLastRootAdmin            = errors.New("cannot remove or lock the last ROOT_ADMIN user")
	ErrSelfLockNotAllowed       = errors.New("cannot lock your own account")
	ErrSelfModificationNotAllow = errors.New("cannot modify your own roles or lock your own account")
	ErrSystemRoleProtected      = errors.New("system role cannot be modified or deleted")
	ErrRoleInUse                = errors.New("role is currently assigned to one or more users")
	ErrWeakPassword             = errors.New("password must be at least 8 characters")
	ErrMFANotEnabled            = errors.New("target user does not have MFA enabled")

	roleCodeRegex = regexp.MustCompile(`^[A-Z0-9_]+$`)

	moduleLabels = map[string]string{
		"PLATFORM_IAM": "Quản trị Nhân sự & Phân quyền Nền tảng",
		"ORGANIZATION": "Quản lý Tổ chức & Subdomain",
		"CATALOG":      "Danh mục Tính năng & Menu nền",
		"PACKAGE":      "Quản lý Gói dịch vụ & Subscription",
		"SUPPORT":      "Quản lý Phiên Hỗ trợ (Support Mode)",
		"AUDIT":        "Nhật ký Kiểm toán Hệ thống",
	}
)

type Service struct {
	db      *gorm.DB
	repo    *Repository
	rdb     *redis.Client
	auditor *audit.Service
}

func NewService(db *gorm.DB, rdb *redis.Client, auditor *audit.Service) *Service {
	return &Service{
		db:      db,
		repo:    NewRepository(db),
		rdb:     rdb,
		auditor: auditor,
	}
}

func dedupeUUIDs(ids []uuid.UUID) []uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
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
		like := "%" + strings.ToLower(strings.TrimSpace(filter.Search)) + "%"
		q = q.Where("LOWER(email) LIKE ? OR LOWER(full_name) LIKE ?", like, like)
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

func (s *Service) GetUserDetail(ctx context.Context, userID uuid.UUID) (*PlatformUser, error) {
	var user PlatformUser
	if err := s.db.WithContext(ctx).Preload("Roles").First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *Service) CreateUser(ctx context.Context, input CreateUserInput) (*PlatformUser, error) {
	if len(strings.TrimSpace(input.Password)) < 8 {
		return nil, ErrWeakPassword
	}

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

	var avatarURL *string
	if input.AvatarURL != nil {
		if trimmed := strings.TrimSpace(*input.AvatarURL); trimmed != "" {
			avatarURL = &trimmed
		}
	}

	roleIDs := dedupeUUIDs(input.RoleIDs)
	user := &PlatformUser{
		ID:                 uuid.New(),
		Email:              email,
		PasswordHash:       string(hashedBytes),
		FullName:           strings.TrimSpace(input.FullName),
		Phone:              input.Phone,
		AvatarURL:          avatarURL,
		Status:             "ACTIVE",
		MustChangePassword: input.MustChangePassword,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}

		for _, roleID := range roleIDs {
			ur := PlatformUserRole{
				PlatformUserID: user.ID,
				PlatformRoleID: roleID,
				AssignedBy:     &input.ActorID,
			}
			if err := tx.Create(&ur).Error; err != nil {
				return err
			}
		}

		return s.logAudit(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "PLATFORM_USER_CREATED",
			ResourceType: "PLATFORM_USER",
			ResourceID:   &user.ID,
			AfterData: map[string]any{
				"id":                   user.ID,
				"email":                user.Email,
				"full_name":            user.FullName,
				"status":               user.Status,
				"must_change_password": user.MustChangePassword,
				"role_ids":             roleIDs,
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

func (s *Service) UpdateUser(ctx context.Context, targetUserID uuid.UUID, input UpdateUserInput) (*PlatformUser, error) {
	var user PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", targetUserID).Error; err != nil {
		return nil, ErrUserNotFound
	}

	updates := map[string]any{"updated_at": time.Now()}
	if input.FullName != nil {
		if trimmed := strings.TrimSpace(*input.FullName); trimmed != "" {
			updates["full_name"] = trimmed
		}
	}
	if input.Phone != nil {
		updates["phone"] = input.Phone
	}
	if input.AvatarURL != nil {
		if trimmed := strings.TrimSpace(*input.AvatarURL); trimmed != "" {
			updates["avatar_url"] = trimmed
		} else {
			updates["avatar_url"] = nil
		}
	}

	oldStatus := user.Status
	newStatus := oldStatus
	if input.Status != nil {
		newStatus = strings.ToUpper(strings.TrimSpace(*input.Status))
		if newStatus != "ACTIVE" && newStatus != "LOCKED" && newStatus != "INACTIVE" {
			return nil, ErrInvalidStatus
		}
		if targetUserID == input.ActorID && (newStatus == "LOCKED" || newStatus == "INACTIVE") {
			return nil, ErrSelfModificationNotAllow
		}
		updates["status"] = newStatus
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if oldStatus == "ACTIVE" && newStatus != "ACTIVE" {
			if err := s.ensureNotLastRootAdmin(tx, targetUserID); err != nil {
				return err
			}
		}

		if err := tx.Model(&user).Updates(updates).Error; err != nil {
			return err
		}

		return s.logAudit(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "PLATFORM_USER_UPDATED",
			ResourceType: "PLATFORM_USER",
			ResourceID:   &targetUserID,
			BeforeData:   map[string]any{"full_name": user.FullName, "phone": user.Phone, "avatar_url": user.AvatarURL, "status": oldStatus},
			AfterData:    updates,
			Reason:       input.Reason,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	if newStatus == "LOCKED" || newStatus == "INACTIVE" {
		s.revokeAllUserSessions(ctx, targetUserID.String())
		s.invalidateUsersAuthzCache(ctx, []string{targetUserID.String()})
	}

	return s.GetUserDetail(ctx, targetUserID)
}

// DeleteUser soft-deletes a platform user, protecting against self-deletion and deleting the last active ROOT_ADMIN.
func (s *Service) DeleteUser(ctx context.Context, targetUserID, actorID uuid.UUID, reason, ip, ua string) error {
	if targetUserID == actorID {
		return ErrSelfModificationNotAllow
	}

	var user PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", targetUserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		return err
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if user.Status == "ACTIVE" {
			if err := s.ensureNotLastRootAdmin(tx, targetUserID); err != nil {
				return err
			}
		}

		if err := tx.Where("platform_user_id = ?", targetUserID).Delete(&PlatformUserRole{}).Error; err != nil {
			return err
		}

		if err := tx.Model(&user).Updates(map[string]any{
			"status":     "INACTIVE",
			"updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}

		if err := tx.Delete(&user).Error; err != nil {
			return err
		}

		return s.logAudit(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "PLATFORM_USER_DELETED",
			ResourceType: "PLATFORM_USER",
			ResourceID:   &targetUserID,
			BeforeData: map[string]any{
				"id":        user.ID,
				"email":     user.Email,
				"full_name": user.FullName,
				"status":    user.Status,
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
	s.invalidateUsersAuthzCache(ctx, []string{targetUserID.String()})
	return nil
}

func (s *Service) UpdateUserRoles(ctx context.Context, targetUserID uuid.UUID, roleIDs []uuid.UUID, actorID uuid.UUID, ip, ua string) error {
	if targetUserID == actorID {
		return ErrSelfModificationNotAllow
	}

	var user PlatformUser
	if err := s.db.WithContext(ctx).Preload("Roles").First(&user, "id = ?", targetUserID).Error; err != nil {
		return ErrUserNotFound
	}

	roleIDs = dedupeUUIDs(roleIDs)

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		hadRoot, err := s.repo.UserHasRootAdmin(tx, targetUserID)
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
			activeOthers, err := s.repo.CountActiveRootAdmins(tx, targetUserID)
			if err != nil {
				return err
			}
			if activeOthers == 0 {
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

		return s.logAudit(tx, audit.AuditEntry{
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

	s.invalidateUsersAuthzCache(ctx, []string{targetUserID.String()})
	return nil
}

func (s *Service) UpdateUserStatus(ctx context.Context, targetUserID uuid.UUID, newStatus, reason string, actorID uuid.UUID, ip, ua string) error {
	newStatus = strings.ToUpper(strings.TrimSpace(newStatus))
	if targetUserID == actorID && (newStatus == "LOCKED" || newStatus == "INACTIVE") {
		return ErrSelfModificationNotAllow
	}

	var user PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", targetUserID).Error; err != nil {
		return ErrUserNotFound
	}

	oldStatus := user.Status

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if oldStatus == "ACTIVE" && newStatus != "ACTIVE" {
			if err := s.ensureNotLastRootAdmin(tx, targetUserID); err != nil {
				return err
			}
		}

		if err := tx.Model(&user).Updates(map[string]any{
			"status":     newStatus,
			"updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}

		return s.logAudit(tx, audit.AuditEntry{
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

	s.invalidateUsersAuthzCache(ctx, []string{targetUserID.String()})
	if newStatus == "LOCKED" || newStatus == "INACTIVE" {
		s.revokeAllUserSessions(ctx, targetUserID.String())
	}

	return nil
}

func (s *Service) ListRoles(ctx context.Context) ([]RoleWithSummary, error) {
	var roles []PlatformRole
	if err := s.db.WithContext(ctx).Preload("Permissions").Order("is_system DESC, created_at ASC").Find(&roles).Error; err != nil {
		return nil, err
	}

	countMap, _ := s.repo.GetRoleUserCounts(ctx)

	result := make([]RoleWithSummary, 0, len(roles))
	for _, r := range roles {
		permCodes := make([]string, 0, len(r.Permissions))
		for _, p := range r.Permissions {
			permCodes = append(permCodes, p.Code)
		}
		result = append(result, RoleWithSummary{
			PlatformRole:    r,
			UserCount:       countMap[r.ID],
			PermissionCount: int64(len(r.Permissions)),
			PermissionCodes: permCodes,
		})
	}
	return result, nil
}

func (s *Service) GetRoleDetail(ctx context.Context, roleID uuid.UUID) (*RoleDetailOutput, error) {
	var role PlatformRole
	if err := s.db.WithContext(ctx).Preload("Permissions").First(&role, "id = ?", roleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRoleNotFound
		}
		return nil, err
	}

	permIDs := make([]uuid.UUID, 0, len(role.Permissions))
	for _, p := range role.Permissions {
		permIDs = append(permIDs, p.ID)
	}

	users, err := s.repo.GetUsersByRoleID(ctx, roleID)
	if err != nil {
		return nil, err
	}

	return &RoleDetailOutput{
		PlatformRole:  role,
		PermissionIDs: permIDs,
		UserCount:     int64(len(users)),
		Users:         users,
	}, nil
}

func (s *Service) ListPermissions(ctx context.Context) ([]PlatformPermission, error) {
	var perms []PlatformPermission
	if err := s.db.WithContext(ctx).Order("module ASC, code ASC").Find(&perms).Error; err != nil {
		return nil, err
	}
	return perms, nil
}

func (s *Service) ListPermissionsGrouped(ctx context.Context) ([]PermissionModuleGroup, error) {
	perms, err := s.ListPermissions(ctx)
	if err != nil {
		return nil, err
	}

	groupIndex := make(map[string]int)
	groups := make([]PermissionModuleGroup, 0)
	for _, p := range perms {
		idx, ok := groupIndex[p.Module]
		if !ok {
			label := moduleLabels[p.Module]
			if label == "" {
				label = p.Module
			}
			idx = len(groups)
			groupIndex[p.Module] = idx
			groups = append(groups, PermissionModuleGroup{
				Module:      p.Module,
				ModuleLabel: label,
				Permissions: []PermissionItem{},
			})
		}
		groups[idx].Permissions = append(groups[idx].Permissions, PermissionItem{
			ID:          p.ID,
			Code:        p.Code,
			Name:        p.Name,
			Description: p.Description,
		})
	}
	return groups, nil
}

func (s *Service) CreateRole(ctx context.Context, input CreateRoleInput) (*PlatformRole, error) {
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if !roleCodeRegex.MatchString(code) {
		return nil, ErrInvalidRoleCodeFormat
	}

	var existing int64
	s.db.WithContext(ctx).Model(&PlatformRole{}).Where("UPPER(code) = ?", code).Count(&existing)
	if existing > 0 {
		return nil, ErrRoleCodeTaken
	}

	permIDs := dedupeUUIDs(input.PermissionIDs)
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

		for _, pid := range permIDs {
			rp := PlatformRolePermission{
				PlatformRoleID:       role.ID,
				PlatformPermissionID: pid,
			}
			if err := tx.Create(&rp).Error; err != nil {
				return err
			}
		}

		return s.logAudit(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "PLATFORM_ROLE_CREATED",
			ResourceType: "PLATFORM_ROLE",
			ResourceID:   &role.ID,
			AfterData: map[string]any{
				"role":           role,
				"permission_ids": permIDs,
			},
			IPAddress: input.IPAddress,
			UserAgent: input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	_ = s.db.WithContext(ctx).Preload("Permissions").First(role, "id = ?", role.ID).Error
	return role, nil
}

func (s *Service) UpdateRole(ctx context.Context, roleID uuid.UUID, input UpdateRoleInput) (*PlatformRole, error) {
	var role PlatformRole
	if err := s.db.WithContext(ctx).Preload("Permissions").First(&role, "id = ?", roleID).Error; err != nil {
		return nil, ErrRoleNotFound
	}

	updates := map[string]any{
		"name":        strings.TrimSpace(input.Name),
		"description": input.Description,
		"updated_at":  time.Now(),
	}

	if input.Code != nil {
		newCode := strings.ToUpper(strings.TrimSpace(*input.Code))
		if !roleCodeRegex.MatchString(newCode) {
			return nil, ErrInvalidRoleCodeFormat
		}
		if newCode != strings.ToUpper(role.Code) {
			if role.IsSystem {
				return nil, ErrSystemRoleProtected
			}
			var existing int64
			s.db.WithContext(ctx).Model(&PlatformRole{}).Where("UPPER(code) = ? AND id != ?", newCode, roleID).Count(&existing)
			if existing > 0 {
				return nil, ErrRoleCodeTaken
			}
			updates["code"] = newCode
		}
	}

	syncPermissions := input.PermissionIDs != nil
	permIDs := dedupeUUIDs(input.PermissionIDs)
	var affectedUserIDs []string

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&role).Updates(updates).Error; err != nil {
			return err
		}

		if syncPermissions {
			if err := tx.Where("platform_role_id = ?", roleID).Delete(&PlatformRolePermission{}).Error; err != nil {
				return err
			}
			for _, pid := range permIDs {
				rp := PlatformRolePermission{
					PlatformRoleID:       roleID,
					PlatformPermissionID: pid,
				}
				if err := tx.Create(&rp).Error; err != nil {
					return err
				}
			}
		}

		var err error
		affectedUserIDs, err = s.repo.GetUserIDsByRoleID(tx, roleID)
		if err != nil {
			return err
		}

		return s.logAudit(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "PLATFORM_ROLE_UPDATED",
			ResourceType: "PLATFORM_ROLE",
			ResourceID:   &roleID,
			AfterData: map[string]any{
				"updates":        updates,
				"permission_ids": permIDs,
			},
			IPAddress: input.IPAddress,
			UserAgent: input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	if syncPermissions {
		s.invalidateUsersAuthzCache(ctx, affectedUserIDs)
	}

	_ = s.db.WithContext(ctx).Preload("Permissions").First(&role, "id = ?", roleID).Error
	return &role, nil
}

func (s *Service) DeleteRole(ctx context.Context, roleID, actorID uuid.UUID, ip, ua string) error {
	var role PlatformRole
	if err := s.db.WithContext(ctx).First(&role, "id = ?", roleID).Error; err != nil {
		return ErrRoleNotFound
	}
	if role.IsSystem {
		return ErrSystemRoleProtected
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		userCount, err := s.repo.CountUsersByRoleID(tx, roleID)
		if err != nil {
			return err
		}
		if userCount > 0 {
			return ErrRoleInUse
		}

		if err := tx.Where("platform_role_id = ?", roleID).Delete(&PlatformRolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&role).Error; err != nil {
			return err
		}

		return s.logAudit(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "PLATFORM_ROLE_DELETED",
			ResourceType: "PLATFORM_ROLE",
			ResourceID:   &roleID,
			BeforeData:   role,
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
}

func (s *Service) UpdateRolePermissions(ctx context.Context, roleID uuid.UUID, permissionIDs []uuid.UUID, permissionCodes []string, actorID uuid.UUID, ip, ua string) error {
	var role PlatformRole
	if err := s.db.WithContext(ctx).Preload("Permissions").First(&role, "id = ?", roleID).Error; err != nil {
		return ErrRoleNotFound
	}

	resolvedIDs := dedupeUUIDs(permissionIDs)
	seen := make(map[uuid.UUID]bool, len(resolvedIDs))
	for _, id := range resolvedIDs {
		seen[id] = true
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

		var err error
		affectedUserIDs, err = s.repo.GetUserIDsByRoleID(tx, roleID)
		if err != nil {
			return err
		}

		return s.logAudit(tx, audit.AuditEntry{
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

	s.invalidateUsersAuthzCache(ctx, affectedUserIDs)
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

		return s.logAudit(tx, audit.AuditEntry{
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

		return s.logAudit(tx, audit.AuditEntry{
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

	s.revokeAllUserSessions(ctx, targetUserID.String())
	return nil
}

func (s *Service) ensureNotLastRootAdmin(tx *gorm.DB, targetUserID uuid.UUID) error {
	hasRoot, err := s.repo.UserHasRootAdmin(tx, targetUserID)
	if err != nil || !hasRoot {
		return err
	}
	activeOthers, err := s.repo.CountActiveRootAdmins(tx, targetUserID)
	if err != nil {
		return err
	}
	if activeOthers == 0 {
		return ErrLastRootAdmin
	}
	return nil
}

func (s *Service) logAudit(tx *gorm.DB, entry audit.AuditEntry) error {
	if s.auditor == nil {
		return nil
	}
	return s.auditor.Log(tx, entry)
}

func (s *Service) invalidateUsersAuthzCache(ctx context.Context, userIDs []string) {
	if s.rdb == nil || len(userIDs) == 0 {
		return
	}
	pipe := s.rdb.Pipeline()
	for _, uid := range userIDs {
		pipe.Del(ctx, rediskeys.PlatformUserAuthzKey(uid))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		fmt.Printf("[WARN] failed to invalidate platform user authz cache: %v\n", err)
	}
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
