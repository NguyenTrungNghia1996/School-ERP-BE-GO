package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"school-erp/internal/platform/audit"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrCodeAlreadyExists         = errors.New("catalog code already exists")
	ErrFeatureNotFound           = errors.New("feature not found")
	ErrPermissionNotFound        = errors.New("permission not found")
	ErrRouteNotFound             = errors.New("tenant route not found")
	ErrMenuNotFound              = errors.New("tenant base menu not found")
	ErrCoreFeatureProtected      = errors.New("core feature cannot be deleted")
	ErrResourceInUse             = errors.New("catalog resource is currently in use")
	ErrMenuHasChildren           = errors.New("menu node still has child items")
	ErrInvalidFeatureCodeFormat  = errors.New("feature code must match ^[A-Z0-9_]+$")
	ErrInvalidPermCodeFormat     = errors.New("permission code must match ^[a-z0-9_]+:[a-z0-9_]+$")
	ErrFeatureInUseCannotModify  = errors.New("feature is already in use and cannot change code")
	ErrItemMenuRequiresRoute     = errors.New("menu of type ITEM requires a valid route_id")
)

var (
	featureCodeRegex = regexp.MustCompile(`^[A-Z0-9_]+$`)
	permCodeRegex    = regexp.MustCompile(`^[a-z0-9_]+:[a-z0-9_]+$`)
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

type ListFeaturesFilter struct {
	Module   string
	IsActive string
	Search   string
}

func (s *Service) ListFeatures(ctx context.Context, filter ListFeaturesFilter) ([]Feature, error) {
	q := s.db.WithContext(ctx).Model(&Feature{})
	if filter.Module != "" {
		q = q.Where("UPPER(module) = ?", strings.ToUpper(strings.TrimSpace(filter.Module)))
	}
	if filter.IsActive == "true" || filter.IsActive == "1" {
		q = q.Where("is_active = ?", true)
	} else if filter.IsActive == "false" || filter.IsActive == "0" {
		q = q.Where("is_active = ?", false)
	}
	if filter.Search != "" {
		like := "%" + strings.TrimSpace(filter.Search) + "%"
		q = q.Where("code ILIKE ? OR name ILIKE ?", like, like)
	}

	var features []Feature
	if err := q.Order("is_core DESC, module ASC, code ASC").Find(&features).Error; err != nil {
		return nil, err
	}
	return features, nil
}

type CreateFeatureInput struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Module      string    `json:"module"`
	Description *string   `json:"description"`
	IsCore      bool      `json:"is_core"`
	IsActive    *bool     `json:"is_active"`
	ActorID     uuid.UUID `json:"-"`
	IPAddress   string    `json:"-"`
	UserAgent   string    `json:"-"`
}

func (s *Service) CreateFeature(ctx context.Context, input CreateFeatureInput) (*Feature, error) {
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if !featureCodeRegex.MatchString(code) {
		return nil, ErrInvalidFeatureCodeFormat
	}

	var count int64
	s.db.WithContext(ctx).Model(&Feature{}).Where("UPPER(code) = ?", code).Count(&count)
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
		IsCore:      input.IsCore,
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

type UpdateFeatureInput struct {
	Code        *string   `json:"code"`
	Name        *string   `json:"name"`
	Module      *string   `json:"module"`
	Description *string   `json:"description"`
	IsCore      *bool     `json:"is_core"`
	IsActive    *bool     `json:"is_active"`
	ActorID     uuid.UUID `json:"-"`
	IPAddress   string    `json:"-"`
	UserAgent   string    `json:"-"`
}

func (s *Service) UpdateFeature(ctx context.Context, featureID uuid.UUID, input UpdateFeatureInput) (*Feature, error) {
	var feature Feature
	if err := s.db.WithContext(ctx).First(&feature, "id = ?", featureID).Error; err != nil {
		return nil, ErrFeatureNotFound
	}

	beforeSnap := feature
	updates := map[string]any{"updated_at": time.Now()}

	if input.Code != nil {
		newCode := strings.ToUpper(strings.TrimSpace(*input.Code))
		if newCode != "" && newCode != feature.Code {
			if !featureCodeRegex.MatchString(newCode) {
				return nil, ErrInvalidFeatureCodeFormat
			}
			// Không cho sửa code nếu Feature đã được gán vào service_package_version hoặc permissions
			var pkgCount, permCount int64
			s.db.WithContext(ctx).Table("service_package_features").Where("feature_id = ?", featureID).Count(&pkgCount)
			s.db.WithContext(ctx).Model(&TenantPermission{}).Where("feature_id = ?", featureID).Count(&permCount)
			if pkgCount > 0 || permCount > 0 {
				return nil, ErrFeatureInUseCannotModify
			}
			updates["code"] = newCode
			feature.Code = newCode
		}
	}
	if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
		name := strings.TrimSpace(*input.Name)
		updates["name"] = name
		feature.Name = name
	}
	if input.Module != nil && strings.TrimSpace(*input.Module) != "" {
		mod := strings.ToUpper(strings.TrimSpace(*input.Module))
		updates["module"] = mod
		feature.Module = mod
	}
	if input.Description != nil {
		updates["description"] = input.Description
		feature.Description = input.Description
	}
	if input.IsCore != nil {
		updates["is_core"] = *input.IsCore
		feature.IsCore = *input.IsCore
	}
	if input.IsActive != nil {
		updates["is_active"] = *input.IsActive
		feature.IsActive = *input.IsActive
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Feature{}).Where("id = ?", featureID).Updates(updates).Error; err != nil {
			return err
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "CATALOG_FEATURE_UPDATED",
			ResourceType: "FEATURE",
			ResourceID:   &featureID,
			BeforeData:   beforeSnap,
			AfterData:    feature,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	return &feature, nil
}

type ListPermissionsFilter struct {
	FeatureID   string
	Resource    string
	IsSensitive string
}

func (s *Service) ListPermissions(ctx context.Context, filter ListPermissionsFilter) ([]TenantPermission, error) {
	q := s.db.WithContext(ctx).Model(&TenantPermission{}).Preload("Feature")
	if filter.FeatureID != "" {
		q = q.Where("feature_id = ?", filter.FeatureID)
	}
	if filter.Resource != "" {
		q = q.Where("LOWER(resource) = ?", strings.ToLower(strings.TrimSpace(filter.Resource)))
	}
	if filter.IsSensitive == "true" || filter.IsSensitive == "1" {
		q = q.Where("is_sensitive = ?", true)
	} else if filter.IsSensitive == "false" || filter.IsSensitive == "0" {
		q = q.Where("is_sensitive = ?", false)
	}

	var perms []TenantPermission
	if err := q.Order("resource ASC, code ASC").Find(&perms).Error; err != nil {
		return nil, err
	}
	return perms, nil
}

type CreatePermissionInput struct {
	FeatureID   uuid.UUID `json:"feature_id"`
	Code        string    `json:"code"`
	Resource    string    `json:"resource"`
	Action      string    `json:"action"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	IsSensitive bool      `json:"is_sensitive"`
	IsActive    *bool     `json:"is_active"`
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
	if !permCodeRegex.MatchString(code) {
		return nil, ErrInvalidPermCodeFormat
	}

	parts := strings.SplitN(code, ":", 2)
	resource := strings.ToLower(strings.TrimSpace(input.Resource))
	if resource == "" {
		resource = parts[0]
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action == "" {
		action = parts[1]
	}

	var count int64
	s.db.WithContext(ctx).Model(&TenantPermission{}).Where("LOWER(code) = ?", code).Count(&count)
	if count > 0 {
		return nil, ErrCodeAlreadyExists
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	perm := &TenantPermission{
		ID:          uuid.New(),
		FeatureID:   input.FeatureID,
		Code:        code,
		Resource:    resource,
		Action:      action,
		Name:        strings.TrimSpace(input.Name),
		Description: input.Description,
		IsSensitive: input.IsSensitive,
		IsActive:    isActive,
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

	perm.Feature = &feature
	return perm, nil
}

type UpdatePermissionInput struct {
	Code        *string   `json:"code"`
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	IsSensitive *bool     `json:"is_sensitive"`
	IsActive    *bool     `json:"is_active"`
	ActorID     uuid.UUID `json:"-"`
	IPAddress   string    `json:"-"`
	UserAgent   string    `json:"-"`
}

func (s *Service) UpdatePermission(ctx context.Context, permID uuid.UUID, input UpdatePermissionInput) (*TenantPermission, error) {
	var perm TenantPermission
	if err := s.db.WithContext(ctx).Preload("Feature").First(&perm, "id = ?", permID).Error; err != nil {
		return nil, ErrPermissionNotFound
	}

	if input.Code != nil && strings.ToLower(strings.TrimSpace(*input.Code)) != perm.Code {
		return nil, ErrFeatureInUseCannotModify
	}

	beforeSnap := perm
	updates := map[string]any{"updated_at": time.Now()}
	if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
		name := strings.TrimSpace(*input.Name)
		updates["name"] = name
		perm.Name = name
	}
	if input.Description != nil {
		updates["description"] = input.Description
		perm.Description = input.Description
	}
	if input.IsSensitive != nil {
		updates["is_sensitive"] = *input.IsSensitive
		perm.IsSensitive = *input.IsSensitive
	}
	if input.IsActive != nil {
		updates["is_active"] = *input.IsActive
		perm.IsActive = *input.IsActive
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&TenantPermission{}).Where("id = ?", permID).Updates(updates).Error; err != nil {
			return err
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "CATALOG_PERMISSION_UPDATED",
			ResourceType: "TENANT_PERMISSION",
			ResourceID:   &permID,
			BeforeData:   beforeSnap,
			AfterData:    perm,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	return &perm, nil
}

func (s *Service) ListRoutes(ctx context.Context) ([]TenantRoute, error) {
	var routes []TenantRoute
	if err := s.db.WithContext(ctx).Order("route_key ASC").Find(&routes).Error; err != nil {
		return nil, err
	}
	return routes, nil
}

func (s *Service) GetTenantBaseMenuTree(ctx context.Context) ([]TenantMenuTreeNode, error) {
	var items []TenantBaseMenu
	if err := s.db.WithContext(ctx).
		Preload("Route").
		Preload("Feature").
		Order("sort_order ASC, created_at ASC").
		Find(&items).Error; err != nil {
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

		var reqPerms []string
		_ = json.Unmarshal(item.RequiredPermissions, &reqPerms)
		if reqPerms == nil {
			reqPerms = []string{}
		}

		var portals []string
		_ = json.Unmarshal(item.TargetPortals, &portals)
		if portals == nil {
			portals = []string{"SCHOOL_ADMIN"}
		}

		var pathPtr *string
		if item.Route != nil {
			p := item.Route.Path
			pathPtr = &p
		}

		var featCodePtr *string
		if item.Feature != nil {
			fc := item.Feature.Code
			featCodePtr = &fc
		}

		nodes = append(nodes, TenantMenuTreeNode{
			ID:                  item.ID,
			ParentID:            item.ParentID,
			Key:                 item.MenuKey,
			Label:               item.Label,
			Icon:                item.Icon,
			Type:                item.MenuType,
			RouteID:             item.RouteID,
			Path:                pathPtr,
			FeatureID:           item.FeatureID,
			FeatureCode:         featCodePtr,
			RequiredPermissions: reqPerms,
			PermissionMatchMode: item.PermissionMatchMode,
			TargetPortals:       portals,
			IsMandatory:         item.IsMandatory,
			SortOrder:           item.SortOrder,
			IsActive:            item.IsActive,
			Children:            children,
		})
	}
	return nodes
}

type CreateMenuInput struct {
	ParentID            *uuid.UUID `json:"parent_id"`
	MenuKey             string     `json:"menu_key"`
	Label               string     `json:"label"`
	Icon                *string    `json:"icon"`
	MenuType            string     `json:"menu_type"`
	RouteID             *uuid.UUID `json:"route_id"`
	FeatureID           *uuid.UUID `json:"feature_id"`
	RequiredPermissions []string   `json:"required_permissions"`
	PermissionMatchMode string     `json:"permission_match_mode"`
	TargetPortals       []string   `json:"target_portals"`
	IsMandatory         bool       `json:"is_mandatory"`
	SortOrder           int        `json:"sort_order"`
	IsActive            *bool      `json:"is_active"`
	ActorID             uuid.UUID  `json:"-"`
	IPAddress           string     `json:"-"`
	UserAgent           string     `json:"-"`
}

func (s *Service) CreateMenuNode(ctx context.Context, input CreateMenuInput) (*TenantBaseMenu, error) {
	menuType := strings.ToUpper(strings.TrimSpace(input.MenuType))
	if menuType == "" {
		menuType = "ITEM"
	}

	if menuType == "ITEM" {
		if input.RouteID == nil || *input.RouteID == uuid.Nil {
			return nil, ErrItemMenuRequiresRoute
		}
		var route TenantRoute
		if err := s.db.WithContext(ctx).First(&route, "id = ?", *input.RouteID).Error; err != nil {
			return nil, ErrRouteNotFound
		}
	}

	matchMode := strings.ToUpper(strings.TrimSpace(input.PermissionMatchMode))
	if matchMode != "ALL" {
		matchMode = "ANY"
	}

	reqPermsBytes := []byte("[]")
	if len(input.RequiredPermissions) > 0 {
		if b, err := json.Marshal(input.RequiredPermissions); err == nil {
			reqPermsBytes = b
		}
	}

	portalsBytes := []byte(`["SCHOOL_ADMIN"]`)
	if len(input.TargetPortals) > 0 {
		if b, err := json.Marshal(input.TargetPortals); err == nil {
			portalsBytes = b
		}
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	node := &TenantBaseMenu{
		ID:                  uuid.New(),
		ParentID:            input.ParentID,
		MenuKey:             strings.TrimSpace(input.MenuKey),
		Label:               strings.TrimSpace(input.Label),
		Icon:                input.Icon,
		MenuType:            menuType,
		RouteID:             input.RouteID,
		FeatureID:           input.FeatureID,
		RequiredPermissions: reqPermsBytes,
		PermissionMatchMode: matchMode,
		TargetPortals:       portalsBytes,
		IsMandatory:         input.IsMandatory,
		SortOrder:           input.SortOrder,
		IsActive:            isActive,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(node).Error; err != nil {
			return err
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "CATALOG_TENANT_MENU_CREATED",
			ResourceType: "TENANT_BASE_MENU",
			ResourceID:   &node.ID,
			AfterData:    node,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	return node, nil
}

type ReorderMenuItem struct {
	ID        uuid.UUID  `json:"id"`
	ParentID  *uuid.UUID `json:"parent_id"`
	SortOrder int        `json:"sort_order"`
}

func (s *Service) ReorderMenus(ctx context.Context, items []ReorderMenuItem, actorID uuid.UUID, ip, ua string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for _, item := range items {
			if err := tx.Model(&TenantBaseMenu{}).Where("id = ?", item.ID).Updates(map[string]any{
				"parent_id":  item.ParentID,
				"sort_order": item.SortOrder,
				"updated_at": now,
			}).Error; err != nil {
				return err
			}
		}

		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "CATALOG_TENANT_MENUS_REORDERED",
			ResourceType: "TENANT_BASE_MENU",
			AfterData:    map[string]any{"reordered_count": len(items)},
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
}

func (s *Service) DeleteFeature(ctx context.Context, featureID, actorID uuid.UUID, ip, ua string) error {
	var f Feature
	if err := s.db.WithContext(ctx).First(&f, "id = ?", featureID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrFeatureNotFound
		}
		return err
	}
	if f.IsCore {
		return ErrCoreFeatureProtected
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pkgCount, permCount, routeCount, menuCount int64
		tx.Table("service_package_features").Where("feature_id = ?", featureID).Count(&pkgCount)
		tx.Model(&TenantPermission{}).Where("feature_id = ?", featureID).Count(&permCount)
		tx.Model(&TenantRoute{}).Where("feature_id = ?", featureID).Count(&routeCount)
		tx.Model(&TenantBaseMenu{}).Where("feature_id = ?", featureID).Count(&menuCount)
		if pkgCount > 0 || permCount > 0 || routeCount > 0 || menuCount > 0 {
			return ErrResourceInUse
		}
		if err := tx.Delete(&f).Error; err != nil {
			return err
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "CATALOG_FEATURE_DELETED",
			ResourceType: "FEATURE",
			ResourceID:   &f.ID,
			BeforeData:   f,
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
}

func (s *Service) DeletePermission(ctx context.Context, permID, actorID uuid.UUID, ip, ua string) error {
	var p TenantPermission
	if err := s.db.WithContext(ctx).First(&p, "id = ?", permID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrPermissionNotFound
		}
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var roleCount int64
		tx.Table("role_permissions").Where("permission_id = ?", permID).Count(&roleCount)
		if roleCount > 0 {
			return ErrResourceInUse
		}
		if err := tx.Delete(&p).Error; err != nil {
			return err
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "CATALOG_PERMISSION_DELETED",
			ResourceType: "PERMISSION",
			ResourceID:   &p.ID,
			BeforeData:   p,
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
}

type CreateRouteInput struct {
	RouteKey  string     `json:"route_key" example:"finance.invoices"`
	Path      string     `json:"path" example:"/finance/invoices"`
	FeatureID *uuid.UUID `json:"feature_id"`
	Name      string     `json:"name" example:"Quản lý Hóa đơn"`
	IsActive  *bool      `json:"is_active"`
	ActorID   uuid.UUID  `json:"-"`
	IPAddress string     `json:"-"`
	UserAgent string     `json:"-"`
}

type UpdateRouteInput struct {
	RouteKey  *string    `json:"route_key"`
	Path      *string    `json:"path"`
	FeatureID *uuid.UUID `json:"feature_id"`
	Name      *string    `json:"name"`
	IsActive  *bool      `json:"is_active"`
	ActorID   uuid.UUID  `json:"-"`
	IPAddress string     `json:"-"`
	UserAgent string     `json:"-"`
}

func (s *Service) CreateRoute(ctx context.Context, input CreateRouteInput) (*TenantRoute, error) {
	routeKey := strings.TrimSpace(input.RouteKey)
	path := strings.TrimSpace(input.Path)
	name := strings.TrimSpace(input.Name)

	var dup int64
	s.db.WithContext(ctx).Model(&TenantRoute{}).Where("route_key = ? OR path = ?", routeKey, path).Count(&dup)
	if dup > 0 {
		return nil, ErrCodeAlreadyExists
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	r := &TenantRoute{
		ID:        uuid.New(),
		RouteKey:  routeKey,
		Path:      path,
		FeatureID: input.FeatureID,
		Name:      name,
		IsActive:  isActive,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(r).Error; err != nil {
			return err
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "CATALOG_ROUTE_CREATED",
			ResourceType: "TENANT_ROUTE",
			ResourceID:   &r.ID,
			AfterData:    r,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Service) UpdateRoute(ctx context.Context, routeID uuid.UUID, input UpdateRouteInput) (*TenantRoute, error) {
	var r TenantRoute
	if err := s.db.WithContext(ctx).First(&r, "id = ?", routeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRouteNotFound
		}
		return nil, err
	}

	updates := map[string]any{}
	if input.RouteKey != nil && strings.TrimSpace(*input.RouteKey) != "" {
		rk := strings.TrimSpace(*input.RouteKey)
		var dup int64
		s.db.WithContext(ctx).Model(&TenantRoute{}).Where("route_key = ? AND id <> ?", rk, routeID).Count(&dup)
		if dup > 0 {
			return nil, ErrCodeAlreadyExists
		}
		updates["route_key"] = rk
	}
	if input.Path != nil && strings.TrimSpace(*input.Path) != "" {
		p := strings.TrimSpace(*input.Path)
		var dup int64
		s.db.WithContext(ctx).Model(&TenantRoute{}).Where("path = ? AND id <> ?", p, routeID).Count(&dup)
		if dup > 0 {
			return nil, ErrCodeAlreadyExists
		}
		updates["path"] = p
	}
	if input.FeatureID != nil {
		updates["feature_id"] = input.FeatureID
	}
	if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
		updates["name"] = strings.TrimSpace(*input.Name)
	}
	if input.IsActive != nil {
		updates["is_active"] = *input.IsActive
	}

	before := r
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&r).Updates(updates).Error; err != nil {
				return err
			}
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "CATALOG_ROUTE_UPDATED",
			ResourceType: "TENANT_ROUTE",
			ResourceID:   &r.ID,
			BeforeData:   before,
			AfterData:    updates,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}
	_ = s.db.WithContext(ctx).First(&r, "id = ?", routeID).Error
	return &r, nil
}

func (s *Service) DeleteRoute(ctx context.Context, routeID, actorID uuid.UUID, ip, ua string) error {
	var r TenantRoute
	if err := s.db.WithContext(ctx).First(&r, "id = ?", routeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRouteNotFound
		}
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var menuCount, cfgCount int64
		tx.Model(&TenantBaseMenu{}).Where("route_id = ?", routeID).Count(&menuCount)
		tx.Table("organization_menu_configs").Where("custom_route_id = ?", routeID).Count(&cfgCount)
		if menuCount > 0 || cfgCount > 0 {
			return ErrResourceInUse
		}
		if err := tx.Delete(&r).Error; err != nil {
			return err
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "CATALOG_ROUTE_DELETED",
			ResourceType: "TENANT_ROUTE",
			ResourceID:   &r.ID,
			BeforeData:   r,
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
}

type UpdateMenuInput struct {
	ParentID            *uuid.UUID `json:"parent_id"`
	Label               *string    `json:"label"`
	Icon                *string    `json:"icon"`
	MenuType            *string    `json:"menu_type"`
	RouteID             *uuid.UUID `json:"route_id"`
	FeatureID           *uuid.UUID `json:"feature_id"`
	RequiredPermissions []string   `json:"required_permissions"`
	PermissionMatchMode *string    `json:"permission_match_mode"`
	TargetPortals       []string   `json:"target_portals"`
	IsMandatory         *bool      `json:"is_mandatory"`
	SortOrder           *int       `json:"sort_order"`
	IsActive            *bool      `json:"is_active"`
	ActorID             uuid.UUID  `json:"-"`
	IPAddress           string     `json:"-"`
	UserAgent           string     `json:"-"`
}

func (s *Service) UpdateMenu(ctx context.Context, menuID uuid.UUID, input UpdateMenuInput) (*TenantBaseMenu, error) {
	var node TenantBaseMenu
	if err := s.db.WithContext(ctx).First(&node, "id = ?", menuID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMenuNotFound
		}
		return nil, err
	}

	updates := map[string]any{"updated_at": time.Now()}
	if input.ParentID != nil {
		if *input.ParentID == uuid.Nil {
			updates["parent_id"] = nil
		} else {
			updates["parent_id"] = input.ParentID
		}
	}
	if input.Label != nil && strings.TrimSpace(*input.Label) != "" {
		updates["label"] = strings.TrimSpace(*input.Label)
	}
	if input.Icon != nil {
		updates["icon"] = input.Icon
	}
	if input.MenuType != nil && strings.TrimSpace(*input.MenuType) != "" {
		updates["menu_type"] = strings.ToUpper(strings.TrimSpace(*input.MenuType))
	}
	if input.RouteID != nil {
		if *input.RouteID == uuid.Nil {
			updates["route_id"] = nil
		} else {
			var routeCount int64
			s.db.WithContext(ctx).Model(&TenantRoute{}).Where("id = ?", *input.RouteID).Count(&routeCount)
			if routeCount == 0 {
				return nil, ErrRouteNotFound
			}
			updates["route_id"] = input.RouteID
		}
	}
	if input.FeatureID != nil {
		if *input.FeatureID == uuid.Nil {
			updates["feature_id"] = nil
		} else {
			updates["feature_id"] = input.FeatureID
		}
	}
	if input.RequiredPermissions != nil {
		if b, err := json.Marshal(input.RequiredPermissions); err == nil {
			updates["required_permissions"] = json.RawMessage(b)
		}
	}
	if input.PermissionMatchMode != nil && strings.TrimSpace(*input.PermissionMatchMode) != "" {
		updates["permission_match_mode"] = strings.ToUpper(strings.TrimSpace(*input.PermissionMatchMode))
	}
	if input.TargetPortals != nil {
		if b, err := json.Marshal(input.TargetPortals); err == nil {
			updates["target_portals"] = json.RawMessage(b)
		}
	}
	if input.IsMandatory != nil {
		updates["is_mandatory"] = *input.IsMandatory
	}
	if input.SortOrder != nil {
		updates["sort_order"] = *input.SortOrder
	}
	if input.IsActive != nil {
		updates["is_active"] = *input.IsActive
	}

	before := node
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&node).Updates(updates).Error; err != nil {
			return err
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &input.ActorID,
			Action:       "CATALOG_TENANT_MENU_UPDATED",
			ResourceType: "TENANT_BASE_MENU",
			ResourceID:   &node.ID,
			BeforeData:   before,
			AfterData:    updates,
			IPAddress:    input.IPAddress,
			UserAgent:    input.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}

	_ = s.db.WithContext(ctx).Preload("Route").Preload("Feature").First(&node, "id = ?", menuID).Error
	return &node, nil
}

func (s *Service) DeleteMenu(ctx context.Context, menuID, actorID uuid.UUID, ip, ua string) error {
	var node TenantBaseMenu
	if err := s.db.WithContext(ctx).First(&node, "id = ?", menuID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMenuNotFound
		}
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var childCount int64
		tx.Model(&TenantBaseMenu{}).Where("parent_id = ?", menuID).Count(&childCount)
		if childCount > 0 {
			return ErrMenuHasChildren
		}
		if err := tx.Delete(&node).Error; err != nil {
			return err
		}
		return s.auditor.Log(tx, audit.AuditEntry{
			ActorType:    "PLATFORM",
			ActorID:      &actorID,
			Action:       "CATALOG_TENANT_MENU_DELETED",
			ResourceType: "TENANT_BASE_MENU",
			ResourceID:   &node.ID,
			BeforeData:   node,
			IPAddress:    ip,
			UserAgent:    ua,
		})
	})
}
