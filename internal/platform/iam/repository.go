package iam

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// CountActiveRootAdmins counts how many ACTIVE platform_users currently hold ROOT_ADMIN.
// When excludeUserID is not uuid.Nil, that user is excluded from the count.
func (r *Repository) CountActiveRootAdmins(tx *gorm.DB, excludeUserID uuid.UUID) (int64, error) {
	var count int64
	q := tx.Table("platform_users u").
		Joins("JOIN platform_user_roles ur ON ur.platform_user_id = u.id").
		Joins("JOIN platform_roles ro ON ro.id = ur.platform_role_id").
		Where("u.deleted_at IS NULL AND u.status = 'ACTIVE' AND UPPER(ro.code) = ?", RootAdminRoleCode)
	if excludeUserID != uuid.Nil {
		q = q.Where("u.id != ?", excludeUserID)
	}
	err := q.Distinct("u.id").Count(&count).Error
	return count, err
}

func (r *Repository) UserHasRootAdmin(tx *gorm.DB, userID uuid.UUID) (bool, error) {
	var count int64
	err := tx.Table("platform_user_roles ur").
		Joins("JOIN platform_roles ro ON ro.id = ur.platform_role_id").
		Where("ur.platform_user_id = ? AND UPPER(ro.code) = ?", userID, RootAdminRoleCode).
		Count(&count).Error
	return count > 0, err
}

func (r *Repository) GetRoleUserCounts(ctx context.Context) (map[uuid.UUID]int64, error) {
	type countRow struct {
		RoleID uuid.UUID
		Count  int64
	}
	var rows []countRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT ur.platform_role_id AS role_id, COUNT(DISTINCT ur.platform_user_id) AS count
		FROM platform_user_roles ur
		JOIN platform_users u ON u.id = ur.platform_user_id AND u.deleted_at IS NULL
		GROUP BY ur.platform_role_id
	`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[uuid.UUID]int64, len(rows))
	for _, row := range rows {
		result[row.RoleID] = row.Count
	}
	return result, nil
}

func (r *Repository) CountUsersByRoleID(tx *gorm.DB, roleID uuid.UUID) (int64, error) {
	var count int64
	err := tx.Table("platform_user_roles ur").
		Joins("JOIN platform_users u ON u.id = ur.platform_user_id AND u.deleted_at IS NULL").
		Where("ur.platform_role_id = ?", roleID).
		Count(&count).Error
	return count, err
}

func (r *Repository) GetUserIDsByRoleID(tx *gorm.DB, roleID uuid.UUID) ([]string, error) {
	var userIDs []string
	err := tx.Model(&PlatformUserRole{}).
		Where("platform_role_id = ?", roleID).
		Distinct("platform_user_id").
		Pluck("platform_user_id", &userIDs).Error
	return userIDs, err
}

func (r *Repository) GetUsersByRoleID(ctx context.Context, roleID uuid.UUID) ([]RoleUserSummary, error) {
	var users []RoleUserSummary
	err := r.db.WithContext(ctx).Raw(`
		SELECT u.id, u.email, u.full_name, u.avatar_url, u.status, u.created_at
		FROM platform_users u
		JOIN platform_user_roles ur ON ur.platform_user_id = u.id
		WHERE ur.platform_role_id = ? AND u.deleted_at IS NULL
		ORDER BY u.created_at DESC
	`, roleID).Scan(&users).Error
	return users, err
}
