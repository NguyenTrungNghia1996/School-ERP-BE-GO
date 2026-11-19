package menu

import (
	"context"
	"encoding/json"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Service struct {
	db      *gorm.DB
	rdb     *redis.Client
	auditor *audit.Service
}

func NewService(db *gorm.DB, rdb *redis.Client, auditor *audit.Service) *Service {
	return &Service{
		db:      db,
		rdb:     rdb,
		auditor: auditor,
	}
}

// GetFullMenuTree reads the full active platform_menus tree using Cache-Aside on Redis.
func (s *Service) GetFullMenuTree(ctx context.Context) ([]AntDMenuNode, error) {
	cacheKey := rediskeys.PlatformMenuTreeKey()

	if s.rdb != nil {
		cached, err := s.rdb.Get(ctx, cacheKey).Result()
		if err == nil && cached != "" {
			var tree []AntDMenuNode
			if err := json.Unmarshal([]byte(cached), &tree); err == nil {
				return tree, nil
			}
		}
	}

	var items []PlatformMenu
	if err := s.db.WithContext(ctx).
		Where("is_active = ?", true).
		Order("sort_order ASC, created_at ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}

	tree := buildAntDTree(items, nil)

	if s.rdb != nil {
		if raw, err := json.Marshal(tree); err == nil {
			_ = s.rdb.Set(ctx, cacheKey, string(raw), rediskeys.TTLPlatformMenuTree).Err()
		}
	}

	return tree, nil
}

func buildAntDTree(items []PlatformMenu, parentID *uuid.UUID) []AntDMenuNode {
	var nodes []AntDMenuNode
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
		children := buildAntDTree(items, &idCopy)

		node := AntDMenuNode{
			ID:                     item.ID,
			Key:                    item.MenuKey,
			Label:                  item.Label,
			Icon:                   item.Icon,
			Path:                   item.RoutePath,
			Type:                   item.MenuType,
			BadgeKey:               item.BadgeKey,
			RequiredPermissionCode: item.RequiredPermissionCode,
			SortOrder:              item.SortOrder,
			Hidden:                 item.IsHidden,
			Children:               children,
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// GetFilteredMenuTree filters the cached menu tree bottom-up based on userPermissions.
func (s *Service) GetFilteredMenuTree(ctx context.Context, userPermissions []string) ([]AntDMenuNode, error) {
	fullTree, err := s.GetFullMenuTree(ctx)
	if err != nil {
		return nil, err
	}

	permSet := make(map[string]bool, len(userPermissions))
	for _, p := range userPermissions {
		permSet[p] = true
	}

	return filterNodesBottomUp(fullTree, permSet), nil
}

func filterNodesBottomUp(nodes []AntDMenuNode, permSet map[string]bool) []AntDMenuNode {
	result := make([]AntDMenuNode, 0)
	for _, node := range nodes {
		filteredChildren := filterNodesBottomUp(node.Children, permSet)

		if node.Type == "SUBMENU" || node.Type == "GROUP" {
			if len(filteredChildren) > 0 {
				clone := node
				clone.Children = filteredChildren
				result = append(result, clone)
			}
			continue
		}

		// ITEM node: keep if required_permission_code is empty or in userPermissions
		reqPerm := ""
		if node.RequiredPermissionCode != nil {
			reqPerm = *node.RequiredPermissionCode
		}
		if reqPerm == "" || permSet[reqPerm] {
			clone := node
			clone.Children = nil
			result = append(result, clone)
		}
	}
	return result
}

// InvalidateMenuTreeCache deletes platform:menu:tree on Redis.
func (s *Service) InvalidateMenuTreeCache(ctx context.Context) {
	if s.rdb != nil {
		_ = s.rdb.Del(ctx, rediskeys.PlatformMenuTreeKey()).Err()
	}
}
