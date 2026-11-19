package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type TenantContext struct {
	OrganizationID string
	SchoolID       string
}

// WithTenantTx mở một transaction và thiết lập SET LOCAL cho PostgreSQL RLS.
// Đảm bảo connection pool không bị rò rỉ tenant context giữa các request.
func WithTenantTx(ctx context.Context, db *gorm.DB, tc TenantContext, fn func(tx *gorm.DB) error) error {
	if tc.OrganizationID == "" {
		return errors.New("MISSING_TENANT_ORGANIZATION_CONTEXT")
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.organization_id', ?, true)", tc.OrganizationID).Error; err != nil {
			return fmt.Errorf("failed to set local organization_id: %w", err)
		}
		if tc.SchoolID != "" {
			if err := tx.Exec("SELECT set_config('app.school_id', ?, true)", tc.SchoolID).Error; err != nil {
				return fmt.Errorf("failed to set local school_id: %w", err)
			}
		}
		return fn(tx)
	})
}
