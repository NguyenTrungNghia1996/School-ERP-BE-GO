package iam

import (
	"time"

	"github.com/google/uuid"
)

type ListUsersFilter struct {
	Search string
	Status string
	RoleID string
	Page   int
	Limit  int
}

type CreateUserInput struct {
	Email              string      `json:"email" example:"cskh.hanoi@school-erp.vn"`
	Password           string      `json:"password" example:"TempPassword@2026"`
	FullName           string      `json:"full_name" example:"Trần Minh Anh"`
	Phone              *string     `json:"phone" example:"0912345678"`
	MustChangePassword bool        `json:"must_change_password" example:"true"`
	RoleIDs            []uuid.UUID `json:"role_ids"`
	ActorID            uuid.UUID   `json:"-"`
	IPAddress          string      `json:"-"`
	UserAgent          string      `json:"-"`
}

type UpdateUserInput struct {
	FullName  *string   `json:"full_name" example:"Trần Minh Anh"`
	Phone     *string   `json:"phone" example:"0912345678"`
	Status    *string   `json:"status" example:"ACTIVE"`
	Reason    string    `json:"reason" example:"Cập nhật thông tin nhân sự"`
	ActorID   uuid.UUID `json:"-"`
	IPAddress string    `json:"-"`
	UserAgent string    `json:"-"`
}

type UpdateUserRolesInput struct {
	RoleIDs []uuid.UUID `json:"role_ids"`
}

type UpdateUserStatusInput struct {
	Status string `json:"status" example:"LOCKED"`
	Reason string `json:"reason" example:"Vi phạm chính sách bảo mật"`
}

type ResetUserPasswordInput struct {
	NewPassword        string `json:"new_password" example:"TempPassword@2026"`
	MustChangePassword *bool  `json:"must_change_password" example:"true"`
	Reason             string `json:"reason" example:"Quên mật khẩu"`
}

type ForceDisableMFAInput struct {
	Reason string `json:"reason" example:"Mất thiết bị xác thực"`
}

type CreateRoleInput struct {
	Code          string      `json:"code" example:"SUPPORT_AGENT"`
	Name          string      `json:"name" example:"Chuyên viên Hỗ trợ"`
	Description   *string     `json:"description" example:"Hỗ trợ vận hành các trường học"`
	PermissionIDs []uuid.UUID `json:"permission_ids"`
	ActorID       uuid.UUID   `json:"-"`
	IPAddress     string      `json:"-"`
	UserAgent     string      `json:"-"`
}

type UpdateRoleInput struct {
	Code          *string     `json:"code,omitempty" example:"SUPPORT_AGENT"`
	Name          string      `json:"name" example:"Chuyên viên Hỗ trợ"`
	Description   *string     `json:"description" example:"Hỗ trợ vận hành các trường học"`
	PermissionIDs []uuid.UUID `json:"permission_ids"`
	ActorID       uuid.UUID   `json:"-"`
	IPAddress     string      `json:"-"`
	UserAgent     string      `json:"-"`
}

type UpdateRolePermissionsInput struct {
	PermissionIDs   []uuid.UUID `json:"permission_ids"`
	PermissionCodes []string    `json:"permission_codes"`
}

type PermissionItem struct {
	ID          uuid.UUID `json:"id" example:"11111111-1111-1111-1111-111111111111"`
	Code        string    `json:"code" example:"platform:user:read"`
	Name        string    `json:"name" example:"Xem danh sách nhân sự nền tảng"`
	Description *string   `json:"description,omitempty"`
}

type PermissionModuleGroup struct {
	Module      string           `json:"module" example:"PLATFORM_IAM"`
	ModuleLabel string           `json:"module_label" example:"Quản trị Nhân sự & Phân quyền Nền tảng"`
	Permissions []PermissionItem `json:"permissions"`
}

type RoleUserSummary struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type RoleDetailOutput struct {
	PlatformRole
	PermissionIDs []uuid.UUID       `json:"permission_ids"`
	UserCount     int64             `json:"user_count"`
	Users         []RoleUserSummary `json:"users"`
}
