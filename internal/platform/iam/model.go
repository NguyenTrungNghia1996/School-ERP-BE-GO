package iam

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const RootAdminRoleCode = "ROOT_ADMIN"
const RootAdminRoleID = "00000000-0000-0000-0000-000000000001"

type PlatformUser struct {
	ID                 uuid.UUID      `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Email              string         `json:"email" gorm:"column:email;type:varchar(255);not null"`
	PasswordHash       string         `json:"-" gorm:"column:password_hash;type:text;not null"`
	FullName           string         `json:"full_name" gorm:"column:full_name;type:varchar(150);not null"`
	Phone              *string        `json:"phone" gorm:"column:phone;type:varchar(20)"`
	AvatarURL          *string        `json:"avatar_url" gorm:"column:avatar_url;type:text"`
	MFASecret          *string        `json:"-" gorm:"column:mfa_secret;type:text"`
	MFAEnabled         bool           `json:"mfa_enabled" gorm:"column:mfa_enabled;not null;default:false"`
	Status             string         `json:"status" gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`
	MustChangePassword bool           `json:"must_change_password" gorm:"column:must_change_password;not null;default:false"`
	LastLoginAt        *time.Time     `json:"last_login_at" gorm:"column:last_login_at"`
	CreatedAt          time.Time      `json:"created_at" gorm:"column:created_at"`
	UpdatedAt          time.Time      `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt          gorm.DeletedAt `json:"-" gorm:"column:deleted_at;index"`
	Roles              []PlatformRole `json:"roles,omitempty" gorm:"many2many:platform_user_roles;joinForeignKey:PlatformUserID;JoinReferences:PlatformRoleID"`
}

func (PlatformUser) TableName() string {
	return "platform_users"
}

type PlatformRole struct {
	ID          uuid.UUID            `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Code        string               `json:"code" gorm:"column:code;type:varchar(50);not null"`
	Name        string               `json:"name" gorm:"column:name;type:varchar(100);not null"`
	Description *string              `json:"description" gorm:"column:description;type:text"`
	IsSystem    bool                 `json:"is_system" gorm:"column:is_system;not null;default:false"`
	CreatedAt   time.Time            `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time            `json:"updated_at" gorm:"column:updated_at"`
	Permissions []PlatformPermission `json:"permissions,omitempty" gorm:"many2many:platform_role_permissions;joinForeignKey:PlatformRoleID;JoinReferences:PlatformPermissionID"`
}

func (PlatformRole) TableName() string {
	return "platform_roles"
}

type PlatformPermission struct {
	ID          uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Code        string    `json:"code" gorm:"column:code;type:varchar(100);not null"`
	Module      string    `json:"module" gorm:"column:module;type:varchar(50);not null"`
	Name        string    `json:"name" gorm:"column:name;type:varchar(150);not null"`
	Description *string   `json:"description" gorm:"column:description;type:text"`
	CreatedAt   time.Time `json:"created_at" gorm:"column:created_at"`
}

func (PlatformPermission) TableName() string {
	return "platform_permissions"
}

type PlatformRolePermission struct {
	PlatformRoleID       uuid.UUID `json:"platform_role_id" gorm:"column:platform_role_id;type:uuid;primaryKey"`
	PlatformPermissionID uuid.UUID `json:"platform_permission_id" gorm:"column:platform_permission_id;type:uuid;primaryKey"`
	CreatedAt            time.Time `json:"created_at" gorm:"column:created_at"`
}

func (PlatformRolePermission) TableName() string {
	return "platform_role_permissions"
}

type PlatformUserRole struct {
	PlatformUserID uuid.UUID  `json:"platform_user_id" gorm:"column:platform_user_id;type:uuid;primaryKey"`
	PlatformRoleID uuid.UUID  `json:"platform_role_id" gorm:"column:platform_role_id;type:uuid;primaryKey"`
	AssignedBy     *uuid.UUID `json:"assigned_by" gorm:"column:assigned_by;type:uuid"`
	CreatedAt      time.Time  `json:"created_at" gorm:"column:created_at"`
}

func (PlatformUserRole) TableName() string {
	return "platform_user_roles"
}

type RoleWithSummary struct {
	PlatformRole
	UserCount       int64    `json:"user_count"`
	PermissionCount int64    `json:"permission_count"`
	PermissionCodes []string `json:"permission_codes"`
}
