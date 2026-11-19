package middleware

import (
	"context"
	"school-erp/config"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/pkg/response"
	"school-erp/internal/platform/billing"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

var dataScopeRank = map[string]int{
	"SELF":           1,
	"ASSIGNED_CLASS": 2,
	"DEPARTMENT":     3,
	"SCHOOL":         4,
	"ORGANIZATION":   5,
}

// HigherDataScope returns whichever scope has wider data visibility.
func HigherDataScope(a, b string) string {
	if dataScopeRank[strings.ToUpper(b)] > dataScopeRank[strings.ToUpper(a)] {
		return strings.ToUpper(b)
	}
	if a == "" {
		return "SCHOOL"
	}
	return strings.ToUpper(a)
}

// LoadMemberPermissionsWithScope loads effective permission -> highest data_scope for a membership,
// intersected with the organization's enabledFeatures, cached as Redis HASH for 15m.
func LoadMemberPermissionsWithScope(ctx context.Context, membershipID string, enabledFeatures []string) (map[string]string, error) {
	if membershipID == "" {
		return map[string]string{}, nil
	}

	cacheKey := rediskeys.TenantMemberAuthzKey(membershipID)
	if config.RedisClient != nil {
		if ex, err := config.RedisClient.Exists(ctx, cacheKey).Result(); err == nil && ex > 0 {
			m, err := config.RedisClient.HGetAll(ctx, cacheKey).Result()
			if err == nil {
				delete(m, rediskeys.EmptyAuthzPlaceholder)
				return m, nil
			}
		}
	}

	type permRow struct {
		PermCode    string `gorm:"column:perm_code"`
		FeatureCode string `gorm:"column:feature_code"`
		DataScope   string `gorm:"column:data_scope"`
	}

	var rows []permRow
	err := config.DB.WithContext(ctx).Raw(`
		SELECT p.code AS perm_code, f.code AS feature_code, r.data_scope AS data_scope
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id AND r.organization_id = ur.organization_id
		JOIN role_permissions rp ON rp.role_id = r.id AND rp.organization_id = r.organization_id
		JOIN permissions p ON p.id = rp.permission_id AND p.is_active = TRUE
		JOIN features f ON f.id = p.feature_id AND f.is_active = TRUE
		WHERE ur.membership_id = ?
	`, membershipID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	featureSet := make(map[string]bool, len(enabledFeatures))
	for _, f := range enabledFeatures {
		featureSet[strings.ToUpper(f)] = true
	}

	result := make(map[string]string)
	for _, row := range rows {
		// Giao quyền với danh sách Feature Entitlement của Organization
		if len(featureSet) > 0 && !featureSet[strings.ToUpper(row.FeatureCode)] {
			continue
		}
		if prev, exists := result[row.PermCode]; exists {
			result[row.PermCode] = HigherDataScope(prev, row.DataScope)
		} else {
			result[row.PermCode] = strings.ToUpper(row.DataScope)
		}
	}

	if config.RedisClient != nil {
		pipe := config.RedisClient.Pipeline()
		pipe.Del(ctx, cacheKey)
		if len(result) == 0 {
			pipe.HSet(ctx, cacheKey, rediskeys.EmptyAuthzPlaceholder, "1")
		} else {
			args := make([]any, 0, len(result)*2)
			for k, v := range result {
				args = append(args, k, v)
			}
			pipe.HSet(ctx, cacheKey, args...)
		}
		pipe.Expire(ctx, cacheKey, rediskeys.TTLTenantAuthz)
		_, _ = pipe.Exec(ctx)
	}

	return result, nil
}

// RequireTenantAccess thực thi chuỗi phân quyền 3 lớp:
// Lớp 1: Subscription Feature Entitlement -> Lớp 2: Role Permission (RBAC) -> Lớp 3: Data Scope.
func RequireTenantAccess(billingSvc *billing.Service, featureCode, permissionCode string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		orgIDStr, _ := c.Locals("tenant_org_id").(string)
		orgID, err := uuid.Parse(orgIDStr)
		if err != nil {
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Lớp 1: Kiểm tra Feature Entitlement của Organization
		var enabledFeatures []string
		if billingSvc != nil {
			ent, err := billingSvc.CalculateEntitlement(ctx, orgID)
			if err != nil {
				return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể kiểm tra gói dịch vụ của Đơn vị", nil)
			}
			enabledFeatures = ent.EnabledFeatures
		}

		if featureCode != "" && !slices.ContainsFunc(enabledFeatures, func(f string) bool {
			return strings.EqualFold(f, featureCode)
		}) {
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeFeatureNotSubscribed, "Đơn vị chưa đăng ký gói tính năng này", fiber.Map{
				"required_feature": featureCode,
			})
		}

		// Nếu không yêu cầu permission cụ thể (chỉ cần authenticated + entitlement)
		if permissionCode == "" {
			c.Locals("data_scope", "SCHOOL")
			return c.Next()
		}

		// Hỗ trợ Support Mode
		if isSupport, _ := c.Locals("tenant_is_support_mode").(bool); isSupport {
			supportPerms, _ := c.Locals("support_permissions").([]string)
			if len(supportPerms) > 0 && !slices.Contains(supportPerms, permissionCode) && !slices.Contains(supportPerms, "*") {
				return response.Error(c, fiber.StatusForbidden, response.ErrCodePermissionDenied, "Phiên hỗ trợ không được cấp quyền này", fiber.Map{
					"required_permission": permissionCode,
				})
			}
			c.Locals("data_scope", "ORGANIZATION")
			return c.Next()
		}

		// Lớp 2: Kiểm tra Permission (RBAC) trên Redis HASH tenant:authz:member:{membership_id}
		membershipID, _ := c.Locals("tenant_membership_id").(string)
		permMap, err := LoadMemberPermissionsWithScope(ctx, membershipID, enabledFeatures)
		if err != nil {
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể kiểm tra quyền hạn thành viên", nil)
		}

		scope, allowed := permMap[permissionCode]
		if !allowed {
			return response.Error(c, fiber.StatusForbidden, response.ErrCodePermissionDenied, "Bạn không có quyền thực hiện thao tác này", fiber.Map{
				"required_permission": permissionCode,
			})
		}

		// Lớp 3: Gắn Data Scope cao nhất vào context
		c.Locals("data_scope", scope)
		return c.Next()
	}
}
