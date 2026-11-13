package seed

import (
	"log"
	"os"
	"school-erp/internal/platform/iam"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Run seeds the initial ROOT_ADMIN user if no ROOT_ADMIN user exists yet.
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

	return EnsureRootAdmin(db, email, password, fullName)
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
