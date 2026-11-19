package menu

import (
	"context"
	"encoding/json"
	"errors"
	"school-erp/internal/pkg/database"
	"school-erp/internal/platform/audit"
	"school-erp/internal/platform/billing"
	"school-erp/internal/platform/catalog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrMandatoryMenuCannotHide = errors.New("mandatory menu cannot be hidden")
	ErrRouteNotFoundOrDenied   = errors.New("route not found or not included in organization entitlement")
	ErrCustomMenuNotFound      = errors.New("custom menu item not found")
)

type Service struct {
	db         *gorm.DB
	billingSvc *billing.Service
	auditor    *audit.Service
}

func NewService(db *gorm.DB, billingSvc *billing.Service, auditor *audit.Service) *Service {
	return &Service{
		db:         db,
		billingSvc: billingSvc,
		auditor:    auditor,
	}
}

func (s *Service) getEnabledFeatureSet(ctx context.Context, orgID uuid.UUID) (map[string]bool, error) {
	featureSet := make(map[string]bool)
	if s.billingSvc == nil {
		return featureSet, nil
	}
	ent, err := s.billingSvc.CalculateEntitlement(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for _, f := range ent.EnabledFeatures {
		featureSet[strings.ToUpper(f)] = true
	}
	return featureSet, nil
}

// GetDynamicMenuTree builds the filtered dynamic menu tree for the authenticated tenant user.
// Filters by Organization Entitlement, Organization visibility overrides (respecting is_mandatory), and User Permissions.
func (s *Service) GetDynamicMenuTree(ctx context.Context, orgID uuid.UUID, userPerms map[string]string) ([]DynamicMenuNode, error) {
	return s.buildTree(ctx, orgID, userPerms, true)
}

// GetManageMenuTree builds the full menu tree (filtered only by Organization Entitlement) for School Admin customization UI.
func (s *Service) GetManageMenuTree(ctx context.Context, orgID uuid.UUID) ([]DynamicMenuNode, error) {
	return s.buildTree(ctx, orgID, nil, false)
}

func (s *Service) buildTree(ctx context.Context, orgID uuid.UUID, userPerms map[string]string, filterForEndUser bool) ([]DynamicMenuNode, error) {
	featureSet, err := s.getEnabledFeatureSet(ctx, orgID)
	if err != nil {
		return nil, err
	}

	var baseMenus []catalog.TenantBaseMenu
	if err := s.db.WithContext(ctx).
		Preload("Route").
		Preload("Feature").
		Where("is_active = TRUE").
		Order("sort_order ASC, created_at ASC").
		Find(&baseMenus).Error; err != nil {
		return nil, err
	}

	var configs []OrganizationMenuConfig
	tc := database.TenantContext{OrganizationID: orgID.String()}
	if err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		return tx.Order("sort_order ASC, created_at ASC").Find(&configs).Error
	}); err != nil {
		return nil, err
	}

	configByBase := make(map[uuid.UUID]OrganizationMenuConfig)
	customConfigs := make([]OrganizationMenuConfig, 0)
	for _, cfg := range configs {
		if cfg.BaseMenuID != nil {
			configByBase[*cfg.BaseMenuID] = cfg
		} else {
			customConfigs = append(customConfigs, cfg)
		}
	}

	// Build base menu nodes indexed by ParentID
	childrenByParent := make(map[uuid.UUID][]catalog.TenantBaseMenu)
	var roots []catalog.TenantBaseMenu
	for _, m := range baseMenus {
		if m.ParentID == nil {
			roots = append(roots, m)
		} else {
			childrenByParent[*m.ParentID] = append(childrenByParent[*m.ParentID], m)
		}
	}

	var buildBaseNode func(m catalog.TenantBaseMenu) (DynamicMenuNode, bool)
	buildBaseNode = func(m catalog.TenantBaseMenu) (DynamicMenuNode, bool) {
		// Entitlement check
		if m.Feature != nil && len(featureSet) > 0 && !featureSet[strings.ToUpper(m.Feature.Code)] {
			return DynamicMenuNode{}, false
		}

		label := m.Label
		icon := ""
		if m.Icon != nil {
			icon = *m.Icon
		}
		sortOrder := m.SortOrder
		isVisible := true
		var cfgID *uuid.UUID

		if cfg, ok := configByBase[m.ID]; ok {
			idCopy := cfg.ID
			cfgID = &idCopy
			if cfg.CustomLabel != nil && strings.TrimSpace(*cfg.CustomLabel) != "" {
				label = *cfg.CustomLabel
			}
			if cfg.CustomIcon != nil {
				icon = *cfg.CustomIcon
			}
			sortOrder = cfg.SortOrder
			if !m.IsMandatory {
				isVisible = cfg.IsVisible
			}
		}

		if filterForEndUser && !isVisible {
			return DynamicMenuNode{}, false
		}

		if filterForEndUser && m.MenuType == "ITEM" && !matchesPermissions(m.RequiredPermissions, m.PermissionMatchMode, userPerms) {
			return DynamicMenuNode{}, false
		}

		routePath := ""
		if m.Route != nil {
			routePath = m.Route.Path
		}

		node := DynamicMenuNode{
			ID:          m.ID,
			ConfigID:    cfgID,
			MenuKey:     m.MenuKey,
			Label:       label,
			Icon:        icon,
			MenuType:    m.MenuType,
			RoutePath:   routePath,
			IsMandatory: m.IsMandatory,
			IsVisible:   isVisible,
			IsCustom:    false,
			SortOrder:   sortOrder,
		}

		for _, child := range childrenByParent[m.ID] {
			if childNode, ok := buildBaseNode(child); ok {
				node.Children = append(node.Children, childNode)
			}
		}
		sort.SliceStable(node.Children, func(i, j int) bool {
			return node.Children[i].SortOrder < node.Children[j].SortOrder
		})

		// Prune empty SUBMENU/GROUP for end-user navigation
		if filterForEndUser && (m.MenuType == "SUBMENU" || m.MenuType == "GROUP") && len(node.Children) == 0 {
			return DynamicMenuNode{}, false
		}
		return node, true
	}

	result := make([]DynamicMenuNode, 0, len(roots)+len(customConfigs))
	for _, r := range roots {
		if node, ok := buildBaseNode(r); ok {
			result = append(result, node)
		}
	}

	// Append custom organization menu items pointing to entitled tenant_routes
	if len(customConfigs) > 0 {
		routeIDs := make([]uuid.UUID, 0, len(customConfigs))
		for _, c := range customConfigs {
			if c.CustomRouteID != nil {
				routeIDs = append(routeIDs, *c.CustomRouteID)
			}
		}
		routeMap := make(map[uuid.UUID]catalog.TenantRoute)
		featureCodeByID := make(map[uuid.UUID]string)
		if len(routeIDs) > 0 {
			var routes []catalog.TenantRoute
			_ = s.db.WithContext(ctx).Where("id IN ? AND is_active = TRUE", routeIDs).Find(&routes).Error
			featIDs := make([]uuid.UUID, 0)
			for _, rt := range routes {
				routeMap[rt.ID] = rt
				if rt.FeatureID != nil {
					featIDs = append(featIDs, *rt.FeatureID)
				}
			}
			if len(featIDs) > 0 {
				var feats []catalog.Feature
				_ = s.db.WithContext(ctx).Where("id IN ?", featIDs).Find(&feats).Error
				for _, f := range feats {
					featureCodeByID[f.ID] = f.Code
				}
			}
		}

		for _, c := range customConfigs {
			if filterForEndUser && !c.IsVisible {
				continue
			}
			if c.CustomRouteID == nil {
				continue
			}
			rt, ok := routeMap[*c.CustomRouteID]
			if !ok {
				continue
			}
			if rt.FeatureID != nil && len(featureSet) > 0 {
				if fCode, ok := featureCodeByID[*rt.FeatureID]; ok && !featureSet[strings.ToUpper(fCode)] {
					continue
				}
			}

			label := rt.Name
			if c.CustomLabel != nil && strings.TrimSpace(*c.CustomLabel) != "" {
				label = *c.CustomLabel
			}
			icon := ""
			if c.CustomIcon != nil {
				icon = *c.CustomIcon
			}
			idCopy := c.ID
			result = append(result, DynamicMenuNode{
				ID:          c.ID,
				ConfigID:    &idCopy,
				MenuKey:     "custom-" + c.ID.String(),
				Label:       label,
				Icon:        icon,
				MenuType:    "ITEM",
				RoutePath:   rt.Path,
				IsMandatory: false,
				IsVisible:   c.IsVisible,
				IsCustom:    true,
				SortOrder:   c.SortOrder,
			})
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].SortOrder < result[j].SortOrder
	})
	return result, nil
}

func matchesPermissions(raw json.RawMessage, mode string, userPerms map[string]string) bool {
	if len(raw) == 0 {
		return true
	}
	var req []string
	if err := json.Unmarshal(raw, &req); err != nil || len(req) == 0 {
		return true
	}
	mode = strings.ToUpper(strings.TrimSpace(mode))
	if mode == "ALL" {
		for _, p := range req {
			if _, ok := userPerms[p]; !ok {
				return false
			}
		}
		return true
	}
	for _, p := range req {
		if _, ok := userPerms[p]; ok {
			return true
		}
	}
	return false
}

func (s *Service) CustomizeMenus(ctx context.Context, tc database.TenantContext, items []CustomizeMenuItemInput, actorID uuid.UUID, ip, ua string) error {
	orgID := uuid.MustParse(tc.OrganizationID)

	return database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		for _, item := range items {
			if item.BaseMenuID != nil {
				var base catalog.TenantBaseMenu
				if err := tx.First(&base, "id = ?", *item.BaseMenuID).Error; err != nil {
					return err
				}
				if base.IsMandatory && item.IsVisible != nil && !*item.IsVisible {
					return ErrMandatoryMenuCannotHide
				}

				var cfg OrganizationMenuConfig
				err := tx.Where("organization_id = ? AND base_menu_id = ?", orgID, *item.BaseMenuID).First(&cfg).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					visible := true
					if item.IsVisible != nil {
						visible = *item.IsVisible
					}
					sortOrder := base.SortOrder
					if item.SortOrder != nil {
						sortOrder = *item.SortOrder
					}
					cfg = OrganizationMenuConfig{
						ID:             uuid.New(),
						OrganizationID: orgID,
						BaseMenuID:     item.BaseMenuID,
						CustomLabel:    item.CustomLabel,
						CustomIcon:     item.CustomIcon,
						SortOrder:      sortOrder,
						IsVisible:      visible,
					}
					if err := tx.Create(&cfg).Error; err != nil {
						return err
					}
				} else if err != nil {
					return err
				} else {
					updates := map[string]any{"updated_at": time.Now()}
					if item.CustomLabel != nil {
						updates["custom_label"] = item.CustomLabel
					}
					if item.CustomIcon != nil {
						updates["custom_icon"] = item.CustomIcon
					}
					if item.SortOrder != nil {
						updates["sort_order"] = *item.SortOrder
					}
					if item.IsVisible != nil {
						updates["is_visible"] = *item.IsVisible
					}
					if err := tx.Model(&cfg).Updates(updates).Error; err != nil {
						return err
					}
				}
			} else if item.ConfigID != nil {
				updates := map[string]any{"updated_at": time.Now()}
				if item.CustomLabel != nil {
					updates["custom_label"] = item.CustomLabel
				}
				if item.CustomIcon != nil {
					updates["custom_icon"] = item.CustomIcon
				}
				if item.SortOrder != nil {
					updates["sort_order"] = *item.SortOrder
				}
				if item.IsVisible != nil {
					updates["is_visible"] = *item.IsVisible
				}
				if err := tx.Model(&OrganizationMenuConfig{}).Where("id = ? AND organization_id = ?", *item.ConfigID, orgID).Updates(updates).Error; err != nil {
					return err
				}
			}
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				Action:         "TENANT_MENU_CUSTOMIZED",
				ResourceType:   "ORGANIZATION_MENU_CONFIG",
				AfterData:      map[string]any{"count": len(items)},
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
}

func (s *Service) CreateCustomMenuItem(ctx context.Context, tc database.TenantContext, input CreateCustomMenuInput, actorID uuid.UUID, ip, ua string) (*OrganizationMenuConfig, error) {
	orgID := uuid.MustParse(tc.OrganizationID)
	featureSet, err := s.getEnabledFeatureSet(ctx, orgID)
	if err != nil {
		return nil, err
	}

	var rt catalog.TenantRoute
	if err := s.db.WithContext(ctx).First(&rt, "id = ? AND is_active = TRUE", input.RouteID).Error; err != nil {
		return nil, ErrRouteNotFoundOrDenied
	}
	if rt.FeatureID != nil && len(featureSet) > 0 {
		var feat catalog.Feature
		if err := s.db.WithContext(ctx).First(&feat, "id = ?", *rt.FeatureID).Error; err == nil {
			if !featureSet[strings.ToUpper(feat.Code)] {
				return nil, ErrRouteNotFoundOrDenied
			}
		}
	}

	label := strings.TrimSpace(input.CustomLabel)
	cfg := &OrganizationMenuConfig{
		ID:             uuid.New(),
		OrganizationID: orgID,
		BaseMenuID:     nil,
		ParentConfigID: input.ParentConfigID,
		CustomLabel:    &label,
		CustomIcon:     input.CustomIcon,
		CustomRouteID:  &input.RouteID,
		SortOrder:      input.SortOrder,
		IsVisible:      true,
	}

	err = database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		if err := tx.Create(cfg).Error; err != nil {
			return err
		}
		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				Action:         "TENANT_CUSTOM_MENU_CREATED",
				ResourceType:   "ORGANIZATION_MENU_CONFIG",
				ResourceID:     &cfg.ID,
				AfterData:      cfg,
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// ListEntitledRoutes returns active tenant_routes that belong to the organization's enabled features (or core routes with feature_id IS NULL).
func (s *Service) ListEntitledRoutes(ctx context.Context, orgID uuid.UUID) ([]EntitledRouteItem, error) {
	featureSet, err := s.getEnabledFeatureSet(ctx, orgID)
	if err != nil {
		return nil, err
	}

	var rows []EntitledRouteItem
	if err := s.db.WithContext(ctx).Raw(`
		SELECT tr.id, tr.route_key, tr.path, tr.name, f.code AS feature_code
		FROM tenant_routes tr
		LEFT JOIN features f ON f.id = tr.feature_id AND f.is_active = TRUE
		WHERE tr.is_active = TRUE
		ORDER BY tr.path ASC
	`).Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]EntitledRouteItem, 0, len(rows))
	for _, r := range rows {
		if r.FeatureCode == nil || *r.FeatureCode == "" || len(featureSet) == 0 || featureSet[strings.ToUpper(*r.FeatureCode)] {
			out = append(out, r)
		}
	}
	return out, nil
}

// DeleteCustomMenuItem deletes an organization's custom menu item (base_menu_id IS NULL).
func (s *Service) DeleteCustomMenuItem(ctx context.Context, tc database.TenantContext, configID, actorID uuid.UUID, ip, ua string) error {
	orgID := uuid.MustParse(tc.OrganizationID)

	return database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		var cfg OrganizationMenuConfig
		if err := tx.First(&cfg, "id = ? AND organization_id = ? AND base_menu_id IS NULL", configID, orgID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCustomMenuNotFound
			}
			return err
		}

		if err := tx.Delete(&cfg).Error; err != nil {
			return err
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "TENANT",
				ActorID:        &actorID,
				OrganizationID: &orgID,
				Action:         "TENANT_CUSTOM_MENU_DELETED",
				ResourceType:   "ORGANIZATION_MENU_CONFIG",
				ResourceID:     &configID,
				BeforeData:     cfg,
				IPAddress:      ip,
				UserAgent:      ua,
			})
		}
		return nil
	})
}
