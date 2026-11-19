package iam

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const SchoolAdminRoleCode = "SCHOOL_ADMIN"

type TenantUser struct {
	ID                 uuid.UUID      `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Email              *string        `json:"email" gorm:"column:email;type:varchar(255)"`
	Username           *string        `json:"username" gorm:"column:username;type:varchar(100)"`
	Phone              *string        `json:"phone" gorm:"column:phone;type:varchar(20)"`
	PasswordHash       string         `json:"-" gorm:"column:password_hash;type:text;not null"`
	FullName           string         `json:"full_name" gorm:"column:full_name;type:varchar(150);not null"`
	AvatarURL          *string        `json:"avatar_url" gorm:"column:avatar_url;type:text"`
	Status             string         `json:"status" gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`
	MustChangePassword bool           `json:"must_change_password" gorm:"column:must_change_password;not null;default:false"`
	LastLoginAt        *time.Time     `json:"last_login_at" gorm:"column:last_login_at"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	DeletedAt          gorm.DeletedAt `json:"-" gorm:"column:deleted_at;index"`
}

func (TenantUser) TableName() string {
	return "users"
}

type SchoolMembership struct {
	ID             uuid.UUID    `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID uuid.UUID    `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	SchoolID       uuid.UUID    `json:"school_id" gorm:"column:school_id;type:uuid;not null"`
	UserID         uuid.UUID    `json:"user_id" gorm:"column:user_id;type:uuid;not null"`
	MemberType     string       `json:"member_type" gorm:"column:member_type;type:varchar(30);not null;default:'STAFF'"`
	IsSchoolAdmin  bool         `json:"is_school_admin" gorm:"column:is_school_admin;not null;default:false"`
	Status         string       `json:"status" gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`
	JoinedAt       time.Time    `json:"joined_at" gorm:"column:joined_at;not null;default:now()"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	User           TenantUser   `json:"user" gorm:"foreignKey:UserID"`
	Roles          []TenantRole `json:"roles,omitempty" gorm:"many2many:user_roles;joinForeignKey:MembershipID;JoinReferences:RoleID"`
}

func (SchoolMembership) TableName() string {
	return "school_memberships"
}

type TenantRole struct {
	ID             uuid.UUID   `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID uuid.UUID   `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	SchoolID       *uuid.UUID  `json:"school_id" gorm:"column:school_id;type:uuid"`
	Code           string      `json:"code" gorm:"column:code;type:varchar(60);not null"`
	Name           string      `json:"name" gorm:"column:name;type:varchar(120);not null"`
	Description    *string     `json:"description" gorm:"column:description;type:text"`
	DataScope      string      `json:"data_scope" gorm:"column:data_scope;type:varchar(30);not null;default:'SCHOOL'"`
	IsSystem       bool        `json:"is_system" gorm:"column:is_system;not null;default:false"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	PermissionIDs  []uuid.UUID `json:"permission_ids,omitempty" gorm:"-"`
}

func (TenantRole) TableName() string {
	return "roles"
}

type RolePermission struct {
	OrganizationID uuid.UUID `gorm:"column:organization_id;type:uuid;not null"`
	RoleID         uuid.UUID `gorm:"column:role_id;type:uuid;primaryKey"`
	PermissionID   uuid.UUID `gorm:"column:permission_id;type:uuid;primaryKey"`
	CreatedAt      time.Time `gorm:"column:created_at"`
}

func (RolePermission) TableName() string {
	return "role_permissions"
}

type UserRole struct {
	OrganizationID uuid.UUID  `gorm:"column:organization_id;type:uuid;not null"`
	MembershipID   uuid.UUID  `gorm:"column:membership_id;type:uuid;primaryKey"`
	RoleID         uuid.UUID  `gorm:"column:role_id;type:uuid;primaryKey"`
	AssignedBy     *uuid.UUID `gorm:"column:assigned_by;type:uuid"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
}

func (UserRole) TableName() string {
	return "user_roles"
}

type EntitledPermissionItem struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Resource    string    `json:"resource"`
	Action      string    `json:"action"`
	FeatureCode string    `json:"feature_code"`
	FeatureName string    `json:"feature_name"`
	IsSensitive bool      `json:"is_sensitive"`
}

type CreateMemberInput struct {
	Email      *string     `json:"email" example:"gv.toan@chuvanan.edu.vn"`
	Username   *string     `json:"username" example:"gv_toan"`
	Phone      *string     `json:"phone" example:"0912345678"`
	Password   string      `json:"password" example:"TempPass@2026"`
	FullName   string      `json:"full_name" example:"Nguyễn Văn A"`
	MemberType string      `json:"member_type" example:"TEACHER"`
	RoleIDs    []uuid.UUID `json:"role_ids"`
}

type UpdateMemberRolesInput struct {
	RoleIDs []uuid.UUID `json:"role_ids"`
}

type UpdateMemberStatusInput struct {
	Status string `json:"status" example:"SUSPENDED"`
	Reason string `json:"reason" example:"Tạm ngưng công tác"`
}

type CreateRoleInput struct {
	SchoolID      *uuid.UUID  `json:"school_id"`
	Code          string      `json:"code" example:"TEACHER_HOMEROOM"`
	Name          string      `json:"name" example:"Giáo viên Chủ nhiệm"`
	Description   *string     `json:"description"`
	DataScope     string      `json:"data_scope" example:"ASSIGNED_CLASS"`
	PermissionIDs []uuid.UUID `json:"permission_ids"`
}

type UpdateRoleInput struct {
	Name          string      `json:"name" example:"Giáo viên Chủ nhiệm"`
	Description   *string     `json:"description"`
	DataScope     string      `json:"data_scope" example:"ASSIGNED_CLASS"`
	PermissionIDs []uuid.UUID `json:"permission_ids"`
}
