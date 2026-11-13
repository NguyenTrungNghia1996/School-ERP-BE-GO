package catalog

import (
	"time"

	"github.com/google/uuid"
)

type Feature struct {
	ID          uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Code        string    `json:"code" gorm:"column:code;type:varchar(100);not null"`
	Name        string    `json:"name" gorm:"column:name;type:varchar(150);not null"`
	Module      string    `json:"module" gorm:"column:module;type:varchar(50);not null"`
	Description *string   `json:"description" gorm:"column:description;type:text"`
	IsActive    bool      `json:"is_active" gorm:"column:is_active;not null;default:true"`
	CreatedAt   time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (Feature) TableName() string {
	return "features"
}

type TenantPermission struct {
	ID          uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	FeatureID   uuid.UUID `json:"feature_id" gorm:"column:feature_id;type:uuid;not null"`
	Code        string    `json:"code" gorm:"column:code;type:varchar(100);not null"`
	Name        string    `json:"name" gorm:"column:name;type:varchar(150);not null"`
	Description *string   `json:"description" gorm:"column:description;type:text"`
	CreatedAt   time.Time `json:"created_at" gorm:"column:created_at"`
}

func (TenantPermission) TableName() string {
	return "permissions"
}

type TenantBaseMenu struct {
	ID                     uuid.UUID  `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ParentID               *uuid.UUID `json:"parent_id" gorm:"column:parent_id;type:uuid"`
	FeatureID              *uuid.UUID `json:"feature_id" gorm:"column:feature_id;type:uuid"`
	MenuKey                string     `json:"menu_key" gorm:"column:menu_key;type:varchar(100);not null;unique"`
	Label                  string     `json:"label" gorm:"column:label;type:varchar(120);not null"`
	Icon                   *string    `json:"icon" gorm:"column:icon;type:varchar(60)"`
	RoutePath              *string    `json:"route_path" gorm:"column:route_path;type:varchar(255)"`
	MenuType               string     `json:"menu_type" gorm:"column:menu_type;type:varchar(20);not null;default:'ITEM'"`
	RequiredPermissionCode *string    `json:"required_permission_code" gorm:"column:required_permission_code;type:varchar(100)"`
	SortOrder              int        `json:"sort_order" gorm:"column:sort_order;not null;default:0"`
	IsHidden               bool       `json:"is_hidden" gorm:"column:is_hidden;not null;default:false"`
	IsActive               bool       `json:"is_active" gorm:"column:is_active;not null;default:true"`
	CreatedAt              time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt              time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (TenantBaseMenu) TableName() string {
	return "menus"
}

type TenantMenuTreeNode struct {
	ID                     uuid.UUID            `json:"id,omitempty"`
	FeatureID              *uuid.UUID           `json:"feature_id,omitempty"`
	Key                    string               `json:"key"`
	Label                  string               `json:"label"`
	Icon                   *string              `json:"icon,omitempty"`
	Path                   *string              `json:"path,omitempty"`
	Type                   string               `json:"type"`
	RequiredPermissionCode *string              `json:"required_permission_code,omitempty"`
	SortOrder              int                  `json:"sort_order"`
	Hidden                 bool                 `json:"hidden"`
	IsActive               bool                 `json:"is_active"`
	Children               []TenantMenuTreeNode `json:"children,omitempty"`
}
