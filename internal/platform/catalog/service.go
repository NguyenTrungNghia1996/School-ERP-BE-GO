package catalog

import (
	"context"
	"errors"
	"school-erp/internal/platform/audit"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrCodeAlreadyExists = errors.New("catalog code already exists")
	ErrFeatureNotFound   = errors.New("feature not found")
)

type Service struct {
	db      *gorm.DB
	auditor *audit.Service
}

func NewService(db *gorm.DB, auditor *audit.Service) *Service {
	return &Service{
		db:      db,
		auditor: auditor,
	}
}

func (s *Service) ListFeatures(ctx context.Context) ([]Feature, error) {
	var features []Feature
	if err := s.db.WithContext(ctx).Order("module ASC, code ASC").Find(&features).Error; err != nil {
		return nil, err
	}
	return features, nil
}

type CreateFeatureInput struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Module      string    `json:"module"`
	Description *string   `json:"description"`
	IsActive    *bool     `json:"is_active"`
	ActorID     uuid.UUID `json:"-"`
	IPAddress   string    `json:"-"`
	UserAgent   string    `json:"-"`
}

func (s *Service) CreateFeature(ctx context.Context, input CreateFeatureInput) (*Feature, error) {
	code := strings.ToLower(strings.TrimSpace(input.Code))
	var count int64
	s.db.WithContext(ctx).Model(&Feature{}).Where("LOWER(code) = ?", code).Count(&count)
	if count > 0 {
		return nil, ErrCodeAlreadyExists
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	feature := &Feature{
		ID:          uuid.New(),
		Code:        code,
		Name:        strings.TrimSpace(input.Name),
		Module:      strings.ToUpper(strings.TrimSpace(input.Module)),
		Description: input.Description,
		IsActive:    isActive,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(feature).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "CATALOG_FEATURE_CREATED",
			ResourceType: "FEATURE",
			ResourceID:   &feature.ID,
			AfterData:    feature,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	return feature, nil
}

func (s *Service) ListPermissions(ctx context.Context, featureID string) ([]TenantPermission, error) {
	q := s.db.WithContext(ctx).Model(&TenantPermission{})
	if featureID != "" {
		q = q.Where("feature_id = ?", featureID)
	}
	var perms []TenantPermission
	if err := q.Order("code ASC").Find(&perms).Error; err != nil {
		return nil, err
	}
	return perms, nil
}

type CreatePermissionInput struct {
	FeatureID   uuid.UUID `json:"feature_id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	ActorID     uuid.UUID `json:"-"`
	IPAddress   string    `json:"-"`
	UserAgent   string    `json:"-"`
}

func (s *Service) CreatePermission(ctx context.Context, input CreatePermissionInput) (*TenantPermission, error) {
	var feature Feature
	if err := s.db.WithContext(ctx).First(&feature, "id = ?", input.FeatureID).Error; err != nil {
		return nil, ErrFeatureNotFound
	}

	code := strings.ToLower(strings.TrimSpace(input.Code))
	var count int64
	s.db.WithContext(ctx).Model(&TenantPermission{}).Where("LOWER(code) = ?", code).Count(&count)
	if count > 0 {
		return nil, ErrCodeAlreadyExists
	}

	perm := &TenantPermission{
		ID:          uuid.New(),
		FeatureID:   input.FeatureID,
		Code:        code,
		Name:        strings.TrimSpace(input.Name),
		Description: input.Description,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(perm).Error; err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "CATALOG_PERMISSION_CREATED",
			ResourceType: "TENANT_PERMISSION",
			ResourceID:   &perm.ID,
			AfterData:    perm,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	return perm, nil
}

func (s *Service) GetTenantBaseMenuTree(ctx context.Context) ([]TenantMenuTreeNode, error) {
	var items []TenantBaseMenu
	if err := s.db.WithContext(ctx).Order("sort_order ASC, created_at ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return buildTenantMenuTree(items, nil), nil
}

func buildTenantMenuTree(items []TenantBaseMenu, parentID *uuid.UUID) []TenantMenuTreeNode {
	nodes := make([]TenantMenuTreeNode, 0)
	for _, item := range items {
		match := false
		if parentID == nil && item.ParentID == nil {
			match = true
		} else if parentID != nil && item.ParentID != nil && *parentID == *item.ParentID {
			match = true
		}
		if !match {
			continue
		}

		idCopy := item.ID
		children := buildTenantMenuTree(items, &idCopy)

		nodes = append(nodes, TenantMenuTreeNode{
			ID:                     item.ID,
			FeatureID:              item.FeatureID,
			Key:                    item.MenuKey,
			Label:                  item.Label,
			Icon:                   item.Icon,
			Path:                   item.RoutePath,
			Type:                   item.MenuType,
			RequiredPermissionCode: item.RequiredPermissionCode,
			SortOrder:              item.SortOrder,
			Hidden:                 item.IsHidden,
			IsActive:               item.IsActive,
			Children:               children,
		})
	}
	return nodes
}

func (s *Service) ReplaceTenantBaseMenuTree(ctx context.Context, tree []TenantMenuTreeNode, actorID uuid.UUID, ip, ua string) ([]TenantMenuTreeNode, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&TenantBaseMenu{}).Error; err != nil {
			return err
		}

		if err := insertMenuTreeNodes(tx, tree, nil); err != nil {
			return err
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "CATALOG_TENANT_MENU_TREE_UPDATED",
			ResourceType: "TENANT_BASE_MENU",
			AfterData:    map[string]any{"root_nodes_count": len(tree)},
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
	if err != nil {
		return nil, err
	}

	return s.GetTenantBaseMenuTree(ctx)
}

func insertMenuTreeNodes(tx *gorm.DB, nodes []TenantMenuTreeNode, parentID *uuid.UUID) error {
	for idx, n := range nodes {
		id := n.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		menuType := strings.ToUpper(strings.TrimSpace(n.Type))
		if menuType == "" {
			menuType = "ITEM"
		}
		sortOrder := n.SortOrder
		if sortOrder == 0 {
			sortOrder = idx + 1
		}
		row := TenantBaseMenu{
			ID:                     id,
			ParentID:               parentID,
			FeatureID:              n.FeatureID,
			MenuKey:                strings.TrimSpace(n.Key),
			Label:                  strings.TrimSpace(n.Label),
			Icon:                   n.Icon,
			RoutePath:              n.Path,
			MenuType:               menuType,
			RequiredPermissionCode: n.RequiredPermissionCode,
			SortOrder:              sortOrder,
			IsHidden:               n.Hidden,
			IsActive:               true,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if len(n.Children) > 0 {
			idCopy := row.ID
			if err := insertMenuTreeNodes(tx, n.Children, &idCopy); err != nil {
				return err
			}
		}
	}
	return nil
}
