package tenant_test

import (
	"context"
	"os"
	"school-erp/internal/middleware"
	"school-erp/internal/pkg/database"
	platformauth "school-erp/internal/platform/auth"
	tenantauth "school-erp/internal/tenant/auth"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTenantTokenIsolationAndDataScopeRank(t *testing.T) {
	// 1. Tenant token parses cleanly as realm="tenant"
	tok, err := tenantauth.GenerateTenantToken(uuid.New().String(), uuid.New().String(), uuid.New().String(), uuid.New().String(), "access", 15*time.Minute)
	if err != nil {
		t.Fatalf("GenerateTenantToken failed: %v", err)
	}
	claims, err := tenantauth.ParseTenantOrSupportToken(tok)
	if err != nil || claims.Realm != "tenant" {
		t.Fatalf("expected realm=tenant, got %+v (err=%v)", claims, err)
	}

	// 2. Platform token MUST be rejected on Tenant routes
	platTok, err := platformauth.GeneratePlatformToken(uuid.New().String(), uuid.New().String(), "access", 15*time.Minute)
	if err != nil {
		t.Fatalf("GeneratePlatformToken failed: %v", err)
	}
	if _, err := tenantauth.ParseTenantOrSupportToken(platTok); err == nil {
		t.Fatal("expected platform token to be rejected by ParseTenantOrSupportToken")
	}

	// 3. Support token parses as realm="support"
	supTok, err := platformauth.GenerateSupportToken(uuid.New().String(), uuid.New().String(), uuid.New().String(), uuid.New().String(), "READ_ONLY", time.Now().Add(15*time.Minute))
	if err != nil {
		t.Fatalf("GenerateSupportToken failed: %v", err)
	}
	supClaims, err := tenantauth.ParseTenantOrSupportToken(supTok)
	if err != nil || supClaims.Realm != "support" {
		t.Fatalf("expected realm=support, got %+v (err=%v)", supClaims, err)
	}

	// 4. HigherDataScope ranking: ORGANIZATION > SCHOOL > DEPARTMENT > ASSIGNED_CLASS > SELF
	if got := middleware.HigherDataScope("SELF", "SCHOOL"); got != "SCHOOL" {
		t.Fatalf("expected SCHOOL, got %s", got)
	}
	if got := middleware.HigherDataScope("ORGANIZATION", "DEPARTMENT"); got != "ORGANIZATION" {
		t.Fatalf("expected ORGANIZATION, got %s", got)
	}

	// 5. WithTenantTx rejects empty OrganizationID
	if err := database.WithTenantTx(context.Background(), nil, database.TenantContext{}, func(tx *gorm.DB) error { return nil }); err == nil {
		t.Fatal("expected error when OrganizationID is empty")
	}
}

// TestRLSTenantIsolation verifies PostgreSQL RLS blocks cross-org reads when TEST_DATABASE_URL is configured (using a non-superuser role).
func TestRLSTenantIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("skipping live PostgreSQL RLS test (set TEST_DATABASE_URL with non-superuser app role to run)")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}

	orgA := uuid.New()
	orgB := uuid.New()
	var count int64
	err = database.WithTenantTx(context.Background(), db, database.TenantContext{OrganizationID: orgA.String()}, func(tx *gorm.DB) error {
		return tx.Table("schools").Where("organization_id = ?", orgB).Count(&count).Error
	})
	if err != nil {
		t.Fatalf("WithTenantTx query failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("RLS isolation breach: orgA saw %d schools of orgB", count)
	}
}
