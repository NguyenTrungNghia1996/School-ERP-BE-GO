package menu

import (
	"time"

	"github.com/google/uuid"
)

type OrganizationMenuConfig struct {
	ID             uuid.UUID  `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrganizationID uuid.UUID  `json:"organization_id" gorm:"column:organization_id;type:uuid;not null"`
	BaseMenuID     *uuid.UUID `json:"base_menu_id" gorm:"column:base_menu_id;type:uuid"`
	ParentConfigID *uuid.UUID `json:"parent_config_id" gorm:"column:parent_config_id;type:uuid"`
	CustomLabel    *string    `json:"custom_label" gorm:"column:custom_label;type:varchar(120)"`
	CustomIcon     *string    `json:"custom_icon" gorm:"column:custom_icon;type:varchar(60)"`
	CustomRouteID  *uuid.UUID `json:"custom_route_id" gorm:"column:custom_route_id;type:uuid"`
	SortOrder      int        `json:"sort_order" gorm:"column:sort_order;not null;default:0"`
	IsVisible      bool       `json:"is_visible" gorm:"column:is_visible;not null;default:true"`
	CreatedAt      time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (OrganizationMenuConfig) TableName() string {
	return "organization_menu_configs"
}

type DynamicMenuNode struct {
	ID          uuid.UUID         `json:"id"`
	ConfigID    *uuid.UUID        `json:"config_id,omitempty"`
	MenuKey     string            `json:"key"`
	Label       string            `json:"label"`
	Icon        string            `json:"icon,omitempty"`
	MenuType    string            `json:"menu_type"`
	RoutePath   string            `json:"route_path,omitempty"`
	IsMandatory bool              `json:"is_mandatory"`
	IsVisible   bool              `json:"is_visible"`
	IsCustom    bool              `json:"is_custom"`
	SortOrder   int               `json:"sort_order"`
	Children    []DynamicMenuNode `json:"children,omitempty"`
}

type CustomizeMenuItemInput struct {
	BaseMenuID  *uuid.UUID `json:"base_menu_id"`
	ConfigID    *uuid.UUID `json:"config_id"`
	CustomLabel *string    `json:"custom_label" example:"Quản lý Lớp học"`
	CustomIcon  *string    `json:"custom_icon" example:"BookOutlined"`
	SortOrder   *int       `json:"sort_order" example:"1"`
	IsVisible   *bool      `json:"is_visible" example:"true"`
}

type CustomizeMenusInput struct {
	Items []CustomizeMenuItemInput `json:"items"`
}

type CreateCustomMenuInput struct {
	ParentConfigID *uuid.UUID `json:"parent_config_id"`
	CustomLabel    string     `json:"custom_label" example:"Báo cáo nhanh"`
	CustomIcon     *string    `json:"custom_icon" example:"BarChartOutlined"`
	RouteID        uuid.UUID  `json:"route_id"`
	SortOrder      int        `json:"sort_order" example:"10"`
}

type EntitledRouteItem struct {
	ID          uuid.UUID `json:"id"`
	RouteKey    string    `json:"route_key"`
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	FeatureCode *string   `json:"feature_code,omitempty"`
}
