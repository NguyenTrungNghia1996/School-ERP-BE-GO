package menu

import (
	"time"

	"github.com/google/uuid"
)

type PlatformMenu struct {
	ID                     uuid.UUID  `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ParentID               *uuid.UUID `json:"parent_id" gorm:"column:parent_id;type:uuid"`
	MenuKey                string     `json:"menu_key" gorm:"column:menu_key;type:varchar(100);not null;unique"`
	Label                  string     `json:"label" gorm:"column:label;type:varchar(120);not null"`
	Icon                   *string    `json:"icon" gorm:"column:icon;type:varchar(60)"`
	RoutePath              *string    `json:"route_path" gorm:"column:route_path;type:varchar(255)"`
	MenuType               string     `json:"menu_type" gorm:"column:menu_type;type:varchar(20);not null;default:'ITEM'"`
	RequiredPermissionCode *string    `json:"required_permission_code" gorm:"column:required_permission_code;type:varchar(100)"`
	BadgeKey               *string    `json:"badge_key" gorm:"column:badge_key;type:varchar(50)"`
	SortOrder              int        `json:"sort_order" gorm:"column:sort_order;not null;default:0"`
	IsHidden               bool       `json:"is_hidden" gorm:"column:is_hidden;not null;default:false"`
	IsActive               bool       `json:"is_active" gorm:"column:is_active;not null;default:true"`
	CreatedAt              time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt              time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (PlatformMenu) TableName() string {
	return "platform_menus"
}

// AntDMenuNode represents a node in the menu tree compatible with Nuxt 4 + Ant Design Vue <a-menu>.
type AntDMenuNode struct {
	ID                     uuid.UUID      `json:"id,omitempty"`
	Key                    string         `json:"key"`
	Label                  string         `json:"label"`
	Icon                   *string        `json:"icon,omitempty"`
	Path                   *string        `json:"path,omitempty"`
	Type                   string         `json:"type"`
	BadgeKey               *string        `json:"badge_key,omitempty"`
	RequiredPermissionCode *string        `json:"required_permission_code,omitempty"`
	SortOrder              int            `json:"sort_order,omitempty"`
	Hidden                 bool           `json:"hidden"`
	Children               []AntDMenuNode `json:"children,omitempty"`
}
