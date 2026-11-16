package catalog

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Feature struct {
	ID          uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Code        string    `json:"code" gorm:"column:code;type:varchar(80);not null"`
	Module      string    `json:"module" gorm:"column:module;type:varchar(50);not null"`
	Name        string    `json:"name" gorm:"column:name;type:varchar(150);not null"`
	Description *string   `json:"description" gorm:"column:description;type:text"`
	IsCore      bool      `json:"is_core" gorm:"column:is_core;not null;default:false"`
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
	Feature     *Feature  `json:"feature,omitempty" gorm:"foreignKey:FeatureID"`
	Code        string    `json:"code" gorm:"column:code;type:varchar(100);not null"`
	Resource    string    `json:"resource" gorm:"column:resource;type:varchar(60);not null"`
	Action      string    `json:"action" gorm:"column:action;type:varchar(40);not null"`
	Name        string    `json:"name" gorm:"column:name;type:varchar(150);not null"`
	Description *string   `json:"description" gorm:"column:description;type:text"`
	IsSensitive bool      `json:"is_sensitive" gorm:"column:is_sensitive;not null;default:false"`
	IsActive    bool      `json:"is_active" gorm:"column:is_active;not null;default:true"`
	CreatedAt   time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (TenantPermission) TableName() string {
	return "permissions"
}

type TenantRoute struct {
	ID        uuid.UUID  `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	RouteKey  string     `json:"route_key" gorm:"column:route_key;type:varchar(100);not null;unique"`
	Path      string     `json:"path" gorm:"column:path;type:varchar(255);not null;unique"`
	FeatureID *uuid.UUID `json:"feature_id" gorm:"column:feature_id;type:uuid"`
	Name      string     `json:"name" gorm:"column:name;type:varchar(150);not null"`
	IsActive  bool       `json:"is_active" gorm:"column:is_active;not null;default:true"`
	CreatedAt time.Time  `json:"created_at" gorm:"column:created_at"`
}

func (TenantRoute) TableName() string {
	return "tenant_routes"
}

type TenantBaseMenu struct {
	ID                  uuid.UUID       `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ParentID            *uuid.UUID      `json:"parent_id" gorm:"column:parent_id;type:uuid"`
	MenuKey             string          `json:"menu_key" gorm:"column:menu_key;type:varchar(100);not null;unique"`
	Label               string          `json:"label" gorm:"column:label;type:varchar(120);not null"`
	Icon                *string         `json:"icon" gorm:"column:icon;type:varchar(60)"`
	MenuType            string          `json:"menu_type" gorm:"column:menu_type;type:varchar(20);not null;default:'ITEM'"`
	RouteID             *uuid.UUID      `json:"route_id" gorm:"column:route_id;type:uuid"`
	Route               *TenantRoute    `json:"route,omitempty" gorm:"foreignKey:RouteID"`
	FeatureID           *uuid.UUID      `json:"feature_id" gorm:"column:feature_id;type:uuid"`
	Feature             *Feature        `json:"feature,omitempty" gorm:"foreignKey:FeatureID"`
	RequiredPermissions json.RawMessage `json:"required_permissions" gorm:"column:required_permissions;type:jsonb;not null;default:'[]'" swaggertype:"array,string"`
	PermissionMatchMode string          `json:"permission_match_mode" gorm:"column:permission_match_mode;type:varchar(10);not null;default:'ANY'"`
	TargetPortals       json.RawMessage `json:"target_portals" gorm:"column:target_portals;type:jsonb;not null;default:'[\"SCHOOL_ADMIN\"]'" swaggertype:"array,string"`
	IsMandatory         bool            `json:"is_mandatory" gorm:"column:is_mandatory;not null;default:false"`
	SortOrder           int             `json:"sort_order" gorm:"column:sort_order;not null;default:0"`
	IsActive            bool            `json:"is_active" gorm:"column:is_active;not null;default:true"`
	CreatedAt           time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt           time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (TenantBaseMenu) TableName() string {
	return "menus"
}

type TenantMenuTreeNode struct {
	ID                  uuid.UUID            `json:"id"`
	ParentID            *uuid.UUID           `json:"parent_id,omitempty"`
	Key                 string               `json:"key"`
	Label               string               `json:"label"`
	Icon                *string              `json:"icon,omitempty"`
	Type                string               `json:"type"`
	RouteID             *uuid.UUID           `json:"route_id,omitempty"`
	Path                *string              `json:"path,omitempty"`
	FeatureID           *uuid.UUID           `json:"feature_id,omitempty"`
	FeatureCode         *string              `json:"feature_code,omitempty"`
	RequiredPermissions []string             `json:"required_permissions"`
	PermissionMatchMode string               `json:"permission_match_mode"`
	TargetPortals       []string             `json:"target_portals"`
	IsMandatory         bool                 `json:"is_mandatory"`
	SortOrder           int                  `json:"sort_order"`
	IsActive            bool                 `json:"is_active"`
	Children            []TenantMenuTreeNode `json:"children,omitempty"`
}
