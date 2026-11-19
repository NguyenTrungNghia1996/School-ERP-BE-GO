package iam

import (
	"context"
	"errors"
	"regexp"
	"school-erp/internal/pkg/database"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/audit"
	"school-erp/internal/platform/billing"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrMemberNotFound           = errors.New("school membership not found")
	ErrMemberAlreadyExists      = errors.New("user is already a member of this school")
	ErrRoleNotFound             = errors.New("tenant role not found")
	ErrRoleCodeTaken            = errors.New("role code already exists")
	ErrInvalidRoleCode          = errors.New("role code must match ^[A-Z0-9_]+$")
	ErrInvalidDataScope         = errors.New("invalid data_scope")
	ErrInvalidMemberType        = errors.New("invalid member_type")
	ErrWeakPassword             = errors.New("password must be at least 8 characters")
	ErrPermissionNotEntitled    = errors.New("one or more permissions are not included in organization entitlement")
	ErrLastSchoolAdminProtected = errors.New("cannot remove SCHOOL_ADMIN from the last active school admin")
	ErrSystemRoleProtected      = errors.New("system role cannot be deleted")
	ErrRoleInUse                = errors.New("role is currently assigned to one or more members")
	ErrInvalidMemberStatus      = errors.New("status must be ACTIVE, SUSPENDED or INACTIVE")
	ErrSelfModificationNotAllow = errors.New("cannot suspend or deactivate your own membership")

	roleCodeRegex = regexp.MustCompile(`^[A-Z0-9_]+$`)

	validDataScopes = map[string]bool{
		"ORGANIZATION":   true,
		"SCHOOL":         true,
		"DEPARTMENT":     true,
		"ASSIGNED_CLASS": true,
		"SELF":           true,
	}
	validMemberTypes = map[string]bool{
		"SCHOOL_ADMIN": true,
		"TEACHER":      true,
		"STAFF":        true,
		"STUDENT":      true,
		"GUARDIAN":     true,
	}
)

type Service struct {
	db         *gorm.DB
	rdb        *redis.Client
	billingSvc *billing.Service
	auditor    *audit.Service
}

func NewService(db *gorm.DB, rdb *redis.Client, billingSvc *billing.Service, auditor *audit.Service) *Service {
	return &Service{
		db:         db,
		rdb:        rdb,
		billingSvc: billingSvc,
		auditor:    auditor,
	}
}

func dedupeUUIDs(ids []uuid.UUID) []uuid.UUID {
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

func (s *Service) ListMembers(ctx context.Context, tc database.TenantContext, search, status string, page, limit int) ([]SchoolMembership, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	var members []SchoolMembership
	var total int64

	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		q := tx.Model(&SchoolMembership{}).Where("school_memberships.organization_id = ?", tc.OrganizationID)
		if tc.SchoolID != "" {
			q = q.Where("school_memberships.school_id = ?", tc.SchoolID)
		}
		if status != "" {
			q = q.Where("school_memberships.status = ?", strings.ToUpper(strings.TrimSpace(status)))
		}
		if strings.TrimSpace(search) != "" {
			like := "%" + strings.ToLower(strings.TrimSpace(search)) + "%"
			q = q.Joins("JOIN users u ON u.id = school_memberships.user_id AND u.deleted_at IS NULL").
				Where("LOWER(u.email) LIKE ? OR LOWER(u.full_name) LIKE ? OR LOWER(u.username) LIKE ?", like, like, like)
		}

		if err := q.Count(&total).Error; err != nil {
			return err
		}
		return q.Preload("User").Preload("Roles").
			Order("school_memberships.created_at DESC").
			Offset((page - 1) * limit).Limit(limit).
			Find(&members).Error
	})
	return members, total, err
}

// CreateMember implements Single Account Identity: reuses existing users row if email/phone matches,
// otherwise creates a new users row, then creates school_memberships & user_roles inside WithTenantTx.
func (s *Service) CreateMember(ctx context.Context, tc database.TenantContext, input CreateMemberInput, actorID uuid.UUID, ip, ua string) (*SchoolMembership, error) {
	memberType := strings.ToUpper(strings.TrimSpace(input.MemberType))
	if memberType == "" {
		memberType = "STAFF"
	}
	if !validMemberTypes[memberType] {
		return nil, ErrInvalidMemberType
	}

	orgID := uuid.MustParse(tc.OrganizationID)
	schoolID := uuid.MustParse(tc.SchoolID)
	roleIDs := dedupeUUIDs(input.RoleIDs)

	var emailNorm *string
	if input.Email != nil && strings.TrimSpace(*input.Email) != "" {
		e := strings.ToLower(strings.TrimSpace(*input.Email))
		emailNorm = &e
	}
	var phoneNorm *string
	if input.Phone != nil && strings.TrimSpace(*input.Phone) != "" {
		p := strings.TrimSpace(*input.Phone)
		phoneNorm = &p
	}
	var usernameNorm *string
	if input.Username != nil && strings.TrimSpace(*input.Username) != "" {
		u := strings.ToLower(strings.TrimSpace(*input.Username))
		usernameNorm = &u
	}

	// 1. Reuse existing Single Account Identity if email or phone matches
	var user TenantUser
	var foundExisting bool
	if emailNorm != nil {
		if err := s.db.WithContext(ctx).Where("LOWER(email) = ?", *emailNorm).First(&user).Error; err == nil {
			foundExisting = true
		}
	}
	if !foundExisting && phoneNorm != nil {
		if err := s.db.WithContext(ctx).Where("phone = ?", *phoneNorm).First(&user).Error; err == nil {
			foundExisting = true
		}
	}

	var hashedPass string
	if !foundExisting {
		if len(strings.TrimSpace(input.Password)) < 8 {
			return nil, ErrWeakPassword
		}
		b, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		hashedPass = string(b)
	}

	var membership SchoolMembership
	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		if !foundExisting {
			user = TenantUser{
				ID:                 uuid.New(),
				Email:              emailNorm,
				Username:           usernameNorm,
				Phone:              phoneNorm,
				PasswordHash:       hashedPass,
				FullName:           strings.TrimSpace(input.FullName),
				Status:             "ACTIVE",
				MustChangePassword: true,
			}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
		}

		var existsCount int64
		tx.Model(&SchoolMembership{}).Where("school_id = ? AND user_id = ?", schoolID, user.ID).Count(&existsCount)
		if existsCount > 0 {
			return ErrMemberAlreadyExists
		}

		membership = SchoolMembership{
			ID:             uuid.New(),
			OrganizationID: orgID,
			SchoolID:       schoolID,
			UserID:         user.ID,
			MemberType:     memberType,
			IsSchoolAdmin:  memberType == "SCHOOL_ADMIN",
			Status:         "ACTIVE",
			JoinedAt:       time.Now(),
		}
		if err := tx.Create(&membership).Error; err != nil {
			return err
		}

		for _, rid := range roleIDs {
			ur := UserRole{
				OrganizationID: orgID,
				MembershipID:   membership.ID,
				RoleID:         rid,
				AssignedBy:     &actorID,
			}
			if err := tx.Create(&ur).Error; err != nil {
				return err
			}
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				SchoolID:       &schoolID,
				Action:         "TENANT_MEMBER_CREATED",
				ResourceType:   "SCHOOL_MEMBERSHIP",
				ResourceID:     &membership.ID,
				AfterData:      map[string]any{"user_id": user.ID, "member_type": memberType, "role_ids": roleIDs},
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.TenantUserOrgsKey(user.ID.String())).Err()
	}

	_ = database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		return tx.Preload("User").Preload("Roles").First(&membership, "id = ?", membership.ID).Error
	})
	return &membership, nil
}

func (s *Service) UpdateMemberRoles(ctx context.Context, tc database.TenantContext, membershipID uuid.UUID, roleIDs []uuid.UUID, actorID uuid.UUID, ip, ua string) error {
	orgID := uuid.MustParse(tc.OrganizationID)
	roleIDs = dedupeUUIDs(roleIDs)

	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		var m SchoolMembership
		if err := tx.First(&m, "id = ? AND organization_id = ?", membershipID, orgID).Error; err != nil {
			return ErrMemberNotFound
		}

		// Kiểm tra xem membership hiện có giữ SCHOOL_ADMIN không
		var hadSchoolAdmin int64
		tx.Table("user_roles ur").
			Joins("JOIN roles r ON r.id = ur.role_id").
			Where("ur.membership_id = ? AND UPPER(r.code) = ?", membershipID, SchoolAdminRoleCode).
			Count(&hadSchoolAdmin)

		var willHaveSchoolAdmin int64
		if len(roleIDs) > 0 {
			tx.Model(&TenantRole{}).
				Where("id IN ? AND organization_id = ? AND UPPER(code) = ?", roleIDs, orgID, SchoolAdminRoleCode).
				Count(&willHaveSchoolAdmin)
		}

		// Guard: Cấm gỡ SCHOOL_ADMIN khỏi admin hoạt động cuối cùng của trường
		if hadSchoolAdmin > 0 && willHaveSchoolAdmin == 0 && m.Status == "ACTIVE" {
			var otherAdmins int64
			tx.Table("school_memberships sm").
				Joins("JOIN user_roles ur ON ur.membership_id = sm.id").
				Joins("JOIN roles r ON r.id = ur.role_id").
				Where("sm.school_id = ? AND sm.status = 'ACTIVE' AND sm.id != ? AND UPPER(r.code) = ?", m.SchoolID, membershipID, SchoolAdminRoleCode).
				Distinct("sm.id").
				Count(&otherAdmins)
			if otherAdmins == 0 {
				return ErrLastSchoolAdminProtected
			}
		}

		if err := tx.Where("membership_id = ? AND organization_id = ?", membershipID, orgID).Delete(&UserRole{}).Error; err != nil {
			return err
		}
		for _, rid := range roleIDs {
			ur := UserRole{
				OrganizationID: orgID,
				MembershipID:   membershipID,
				RoleID:         rid,
				AssignedBy:     &actorID,
			}
			if err := tx.Create(&ur).Error; err != nil {
				return err
			}
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				SchoolID:       &m.SchoolID,
				Action:         "TENANT_MEMBER_ROLES_UPDATED",
				ResourceType:   "SCHOOL_MEMBERSHIP",
				ResourceID:     &membershipID,
				AfterData:      map[string]any{"role_ids": roleIDs},
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
	if err != nil {
		return err
	}

	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.TenantMemberAuthzKey(membershipID.String())).Err()
	}
	return nil
}

func (s *Service) ListEntitledPermissions(ctx context.Context, orgID uuid.UUID) ([]EntitledPermissionItem, error) {
	var enabledFeatures []string
	if s.billingSvc != nil {
		ent, err := s.billingSvc.CalculateEntitlement(ctx, orgID)
		if err != nil {
			return nil, err
		}
		enabledFeatures = ent.EnabledFeatures
	}

	if len(enabledFeatures) == 0 {
		return []EntitledPermissionItem{}, nil
	}

	var items []EntitledPermissionItem
	err := s.db.WithContext(ctx).Raw(`
		SELECT p.id, p.code, p.name, p.resource, p.action, f.code AS feature_code, f.name AS feature_name, p.is_sensitive
		FROM permissions p
		JOIN features f ON f.id = p.feature_id AND f.is_active = TRUE
		WHERE p.is_active = TRUE
		  AND UPPER(f.code) IN ?
		ORDER BY f.code ASC, p.code ASC
	`, toUpperSlice(enabledFeatures)).Scan(&items).Error
	if items == nil {
		items = []EntitledPermissionItem{}
	}
	return items, err
}

func (s *Service) validatePermissionsEntitled(ctx context.Context, orgID uuid.UUID, permIDs []uuid.UUID) error {
	if len(permIDs) == 0 {
		return nil
	}
	entitled, err := s.ListEntitledPermissions(ctx, orgID)
	if err != nil {
		return err
	}
	allowed := make(map[uuid.UUID]bool, len(entitled))
	for _, item := range entitled {
		allowed[item.ID] = true
	}
	for _, pid := range permIDs {
		if !allowed[pid] {
			return ErrPermissionNotEntitled
		}
	}
	return nil
}

func (s *Service) ListRoles(ctx context.Context, tc database.TenantContext) ([]TenantRole, error) {
	var roles []TenantRole
	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		if err := tx.Order("is_system DESC, created_at ASC").Find(&roles).Error; err != nil {
			return err
		}
		for i := range roles {
			var pIDs []uuid.UUID
			_ = tx.Model(&RolePermission{}).Where("role_id = ?", roles[i].ID).Pluck("permission_id", &pIDs).Error
			roles[i].PermissionIDs = pIDs
		}
		return nil
	})
	return roles, err
}

func (s *Service) CreateRole(ctx context.Context, tc database.TenantContext, input CreateRoleInput, actorID uuid.UUID, ip, ua string) (*TenantRole, error) {
	orgID := uuid.MustParse(tc.OrganizationID)
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if !roleCodeRegex.MatchString(code) {
		return nil, ErrInvalidRoleCode
	}
	scope := strings.ToUpper(strings.TrimSpace(input.DataScope))
	if scope == "" {
		scope = "SCHOOL"
	}
	if !validDataScopes[scope] {
		return nil, ErrInvalidDataScope
	}

	permIDs := dedupeUUIDs(input.PermissionIDs)
	if err := s.validatePermissionsEntitled(ctx, orgID, permIDs); err != nil {
		return nil, err
	}

	role := &TenantRole{
		ID:             uuid.New(),
		OrganizationID: orgID,
		SchoolID:       input.SchoolID,
		Code:           code,
		Name:           strings.TrimSpace(input.Name),
		Description:    input.Description,
		DataScope:      scope,
		IsSystem:       false,
		PermissionIDs:  permIDs,
	}

	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		var dup int64
		q := tx.Model(&TenantRole{}).Where("organization_id = ? AND UPPER(code) = ?", orgID, code)
		if input.SchoolID == nil {
			q = q.Where("school_id IS NULL")
		} else {
			q = q.Where("school_id = ?", *input.SchoolID)
		}
		q.Count(&dup)
		if dup > 0 {
			return ErrRoleCodeTaken
		}

		if err := tx.Create(role).Error; err != nil {
			return err
		}
		for _, pid := range permIDs {
			rp := RolePermission{
				OrganizationID: orgID,
				RoleID:         role.ID,
				PermissionID:   pid,
			}
			if err := tx.Create(&rp).Error; err != nil {
				return err
			}
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				SchoolID:       input.SchoolID,
				Action:         "TENANT_ROLE_CREATED",
				ResourceType:   "ROLE",
				ResourceID:     &role.ID,
				AfterData:      map[string]any{"role": role, "permission_ids": permIDs},
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return role, nil
}

func (s *Service) UpdateRole(ctx context.Context, tc database.TenantContext, roleID uuid.UUID, input UpdateRoleInput, actorID uuid.UUID, ip, ua string) (*TenantRole, error) {
	orgID := uuid.MustParse(tc.OrganizationID)
	scope := strings.ToUpper(strings.TrimSpace(input.DataScope))
	if scope != "" && !validDataScopes[scope] {
		return nil, ErrInvalidDataScope
	}

	syncPerms := input.PermissionIDs != nil
	permIDs := dedupeUUIDs(input.PermissionIDs)
	if syncPerms {
		if err := s.validatePermissionsEntitled(ctx, orgID, permIDs); err != nil {
			return nil, err
		}
	}

	var role TenantRole
	var affectedMembershipIDs []string

	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		if err := tx.First(&role, "id = ? AND organization_id = ?", roleID, orgID).Error; err != nil {
			return ErrRoleNotFound
		}

		updates := map[string]any{
			"name":        strings.TrimSpace(input.Name),
			"description": input.Description,
			"updated_at":  time.Now(),
		}
		if scope != "" {
			updates["data_scope"] = scope
		}
		if err := tx.Model(&role).Updates(updates).Error; err != nil {
			return err
		}

		if syncPerms {
			if err := tx.Where("role_id = ? AND organization_id = ?", roleID, orgID).Delete(&RolePermission{}).Error; err != nil {
				return err
			}
			for _, pid := range permIDs {
				rp := RolePermission{
					OrganizationID: orgID,
					RoleID:         roleID,
					PermissionID:   pid,
				}
				if err := tx.Create(&rp).Error; err != nil {
					return err
				}
			}
		}

		_ = tx.Model(&UserRole{}).
			Where("role_id = ? AND organization_id = ?", roleID, orgID).
			Distinct("membership_id").
			Pluck("membership_id", &affectedMembershipIDs).Error

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				Action:         "TENANT_ROLE_UPDATED",
				ResourceType:   "ROLE",
				ResourceID:     &roleID,
				AfterData:      map[string]any{"updates": updates, "permission_ids": permIDs},
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return tx.First(&role, "id = ?", roleID).Error
	})
	if err != nil {
		return nil, err
	}

	// Xóa cache Redis của toàn bộ thành viên giữ vai trò này bằng Pipeline
	if s.rdb != nil && len(affectedMembershipIDs) > 0 {
		pipe := s.rdb.Pipeline()
		for _, mid := range affectedMembershipIDs {
			pipe.Del(ctx, rediskeys.TenantMemberAuthzKey(mid))
		}
		_, _ = pipe.Exec(ctx)
	}

	role.PermissionIDs = permIDs
	return &role, nil
}

// DeleteRole deletes a custom tenant role (protects is_system=true and roles currently in use).
func (s *Service) DeleteRole(ctx context.Context, tc database.TenantContext, roleID, actorID uuid.UUID, ip, ua string) error {
	orgID := uuid.MustParse(tc.OrganizationID)

	return database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		var role TenantRole
		if err := tx.First(&role, "id = ? AND organization_id = ?", roleID, orgID).Error; err != nil {
			return ErrRoleNotFound
		}
		if role.IsSystem {
			return ErrSystemRoleProtected
		}

		var assignedCount int64
		tx.Model(&UserRole{}).Where("role_id = ? AND organization_id = ?", roleID, orgID).Count(&assignedCount)
		if assignedCount > 0 {
			return ErrRoleInUse
		}

		if err := tx.Where("role_id = ? AND organization_id = ?", roleID, orgID).Delete(&RolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&role).Error; err != nil {
			return err
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				Action:         "TENANT_ROLE_DELETED",
				ResourceType:   "ROLE",
				ResourceID:     &roleID,
				BeforeData:     role,
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
}

// UpdateMemberStatus updates a school_membership status (ACTIVE/SUSPENDED/INACTIVE),
// blocking self-lockout and protecting the last active SCHOOL_ADMIN of the school.
func (s *Service) UpdateMemberStatus(ctx context.Context, tc database.TenantContext, membershipID uuid.UUID, newStatus, reason string, actorID uuid.UUID, ip, ua string) error {
	orgID := uuid.MustParse(tc.OrganizationID)
	status := strings.ToUpper(strings.TrimSpace(newStatus))
	if status != "ACTIVE" && status != "SUSPENDED" && status != "INACTIVE" {
		return ErrInvalidMemberStatus
	}

	var targetUserID uuid.UUID
	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		var m SchoolMembership
		if err := tx.First(&m, "id = ? AND organization_id = ?", membershipID, orgID).Error; err != nil {
			return ErrMemberNotFound
		}
		targetUserID = m.UserID

		if m.UserID == actorID && status != "ACTIVE" {
			return ErrSelfModificationNotAllow
		}

		if m.Status == "ACTIVE" && status != "ACTIVE" {
			var isSchoolAdmin int64
			tx.Table("user_roles ur").
				Joins("JOIN roles r ON r.id = ur.role_id").
				Where("ur.membership_id = ? AND UPPER(r.code) = ?", membershipID, SchoolAdminRoleCode).
				Count(&isSchoolAdmin)
			if isSchoolAdmin > 0 {
				var otherAdmins int64
				tx.Table("school_memberships sm").
					Joins("JOIN user_roles ur ON ur.membership_id = sm.id").
					Joins("JOIN roles r ON r.id = ur.role_id").
					Where("sm.school_id = ? AND sm.status = 'ACTIVE' AND sm.id != ? AND UPPER(r.code) = ?", m.SchoolID, membershipID, SchoolAdminRoleCode).
					Distinct("sm.id").
					Count(&otherAdmins)
				if otherAdmins == 0 {
					return ErrLastSchoolAdminProtected
				}
			}
		}

		oldStatus := m.Status
		if err := tx.Model(&m).Updates(map[string]any{
			"status":     status,
			"updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				SchoolID:       &m.SchoolID,
				Action:         "TENANT_MEMBER_STATUS_UPDATED",
				ResourceType:   "SCHOOL_MEMBERSHIP",
				ResourceID:     &membershipID,
				BeforeData:     map[string]any{"status": oldStatus},
				AfterData:      map[string]any{"status": status},
				Reason:         reason,
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
	if err != nil {
		return err
	}

	if s.rdb != nil {
		pipe := s.rdb.Pipeline()
		pipe.Del(ctx, rediskeys.TenantMemberAuthzKey(membershipID.String()))
		if targetUserID != uuid.Nil {
			pipe.Del(ctx, rediskeys.TenantUserOrgsKey(targetUserID.String()))
		}
		_, _ = pipe.Exec(ctx)
	}
	return nil
}

func toUpperSlice(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToUpper(s)
	}
	return out
}
