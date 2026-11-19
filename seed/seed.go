package seed

import (
	"log"
	"os"
	"school-erp/internal/platform/iam"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Run seeds the initial ROOT_ADMIN user and a default demo test organization.
func Run(db *gorm.DB) error {
	email := strings.ToLower(strings.TrimSpace(os.Getenv("PLATFORM_ROOT_EMAIL")))
	if email == "" {
		email = "admin@school-erp.vn"
	}
	password := os.Getenv("PLATFORM_ROOT_PASSWORD")
	if password == "" {
		password = "Admin@123456"
	}
	fullName := os.Getenv("PLATFORM_ROOT_NAME")
	if fullName == "" {
		fullName = "Super Admin"
	}

	if err := EnsurePlatformPermissionsAndMenus(db); err != nil {
		return err
	}

	if err := EnsureTenantCatalog(db); err != nil {
		return err
	}

	if err := EnsureRootAdmin(db, email, password, fullName); err != nil {
		return err
	}

	return EnsureDemoOrganization(db)
}

func EnsureRootAdmin(db *gorm.DB, email, password, fullName string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	rootRoleUUID := uuid.MustParse(iam.RootAdminRoleID)

	var user iam.PlatformUser
	err := db.Where("LOWER(email) = ?", email).First(&user).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			return err
		}

		hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}

		user = iam.PlatformUser{
			ID:           uuid.New(),
			Email:        email,
			PasswordHash: string(hashed),
			FullName:     fullName,
			Status:       "ACTIVE",
		}
		if err := db.Create(&user).Error; err != nil {
			return err
		}
		log.Printf("✅ Đã khởi tạo tài khoản ROOT_ADMIN mặc định: %s\n", email)
	}

	var count int64
	db.Model(&iam.PlatformUserRole{}).
		Where("platform_user_id = ? AND platform_role_id = ?", user.ID, rootRoleUUID).
		Count(&count)

	if count == 0 {
		ur := iam.PlatformUserRole{
			PlatformUserID: user.ID,
			PlatformRoleID: rootRoleUUID,
		}
		if err := db.Create(&ur).Error; err != nil {
			return err
		}
	}

	return nil
}

// EnsureDemoOrganization seeds a ready-to-test Organization ("demo") with a main School,
// default SCHOOL_ADMIN account (admin@demo.school-erp.vn / Admin@123456), system role, and active PRIMARY subscription.
func EnsureDemoOrganization(db *gorm.DB) error {
	subdomain := strings.ToLower(strings.TrimSpace(os.Getenv("SEED_DEMO_ORG_SUBDOMAIN")))
	if subdomain == "" {
		subdomain = "demo"
	}
	adminEmail := strings.ToLower(strings.TrimSpace(os.Getenv("SEED_DEMO_ADMIN_EMAIL")))
	if adminEmail == "" {
		adminEmail = "admin@demo.school-erp.vn"
	}
	adminPassword := os.Getenv("SEED_DEMO_ADMIN_PASSWORD")
	if adminPassword == "" {
		adminPassword = "Admin@123456"
	}

	var existingCount int64
	if err := db.Table("organizations").Where("LOWER(subdomain) = ? AND deleted_at IS NULL", subdomain).Count(&existingCount).Error; err != nil {
		return err
	}
	if existingCount > 0 {
		return nil
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	orgID := uuid.New()
	schoolID := uuid.New()
	userID := uuid.New()
	membershipID := uuid.New()
	roleID := uuid.New()
	now := time.Now().UTC()

	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			INSERT INTO organizations (id, code, name, subdomain, contact_email, status, dns_status, approved_at, created_at, updated_at)
			VALUES (?, 'ORG-DEMO', 'Trường THPT Demo Test', ?, ?, 'ACTIVE', 'ACTIVE', ?, ?, ?)
		`, orgID, subdomain, adminEmail, now, now, now).Error; err != nil {
			return err
		}

		// Thiết lập tenant context cho transaction để tương thích PostgreSQL RLS
		if err := tx.Exec("SELECT set_config('app.organization_id', ?, true)", orgID.String()).Error; err != nil {
			return err
		}

		if err := tx.Exec(`
			INSERT INTO schools (id, organization_id, code, name, education_levels, is_main, status, created_at, updated_at)
			VALUES (?, ?, 'MAIN', 'Trường THPT Demo Test - Cơ sở chính', '["HIGH_SCHOOL"]'::jsonb, TRUE, 'ACTIVE', ?, ?)
		`, schoolID, orgID, now, now).Error; err != nil {
			return err
		}

		var existingUserID uuid.UUID
		tx.Table("users").Select("id").Where("LOWER(email) = ? AND deleted_at IS NULL", adminEmail).Scan(&existingUserID)
		if existingUserID != uuid.Nil {
			userID = existingUserID
		} else {
			if err := tx.Exec(`
				INSERT INTO users (id, email, username, password_hash, full_name, status, must_change_password, created_at, updated_at)
				VALUES (?, ?, 'admin_demo', ?, 'Quản trị viên Demo', 'ACTIVE', FALSE, ?, ?)
			`, userID, adminEmail, string(hashed), now, now).Error; err != nil {
				return err
			}
		}

		if err := tx.Exec(`
			INSERT INTO school_memberships (id, organization_id, school_id, user_id, member_type, is_school_admin, status, joined_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'SCHOOL_ADMIN', TRUE, 'ACTIVE', ?, ?, ?)
		`, membershipID, orgID, schoolID, userID, now, now, now).Error; err != nil {
			return err
		}

		if err := tx.Exec(`
			INSERT INTO roles (id, organization_id, school_id, code, name, description, data_scope, is_system, created_at, updated_at)
			VALUES (?, ?, NULL, 'SCHOOL_ADMIN', 'Quản trị viên Trường', 'Vai trò quản trị mặc định của Đơn vị/Trường học', 'ORGANIZATION', TRUE, ?, ?)
		`, roleID, orgID, now, now).Error; err != nil {
			return err
		}

		if err := tx.Exec(`
			INSERT INTO role_permissions (organization_id, role_id, permission_id)
			SELECT ?, ?, p.id
			FROM permissions p
			JOIN features f ON f.id = p.feature_id
			WHERE p.is_active = TRUE
			  AND UPPER(f.code) IN ('CORE_IAM', 'CORE_ORG', 'CORE_ACADEMIC')
			ON CONFLICT DO NOTHING
		`, orgID, roleID).Error; err != nil {
			return err
		}

		if err := tx.Exec(`
			INSERT INTO user_roles (organization_id, membership_id, role_id)
			VALUES (?, ?, ?)
			ON CONFLICT DO NOTHING
		`, orgID, membershipID, roleID).Error; err != nil {
			return err
		}

		// Gán gói PRIMARY mẫu (1 năm) nếu phiên bản gói mẫu tồn tại
		var pkgVerExists int64
		tx.Table("service_package_versions").Where("id = '770e8400-e29b-41d4-a716-446655440098'").Count(&pkgVerExists)
		if pkgVerExists > 0 {
			expiresAt := now.AddDate(1, 0, 0)
			if err := tx.Exec(`
				INSERT INTO organization_subscriptions (id, organization_id, package_version_id, subscription_type, status, started_at, expires_at, auto_renew, notes, created_at, updated_at)
				VALUES (?, ?, '770e8400-e29b-41d4-a716-446655440098', 'PRIMARY', 'ACTIVE', ?, ?, FALSE, 'Gói mặc định cho đơn vị Demo Test', ?, ?)
			`, uuid.New(), orgID, now, expiresAt, now, now).Error; err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	log.Printf("✅ Đã khởi tạo đơn vị test mặc định: subdomain='%s' | admin='%s' | password='%s'\n", subdomain, adminEmail, adminPassword)
	return nil
}
