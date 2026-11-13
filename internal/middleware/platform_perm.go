package middleware

import (
	"context"
	"school-erp/config"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/pkg/response"
	"time"

	"github.com/gofiber/fiber/v2"
)

// LoadAndCacheUserPermissions loads a platform user's permission codes using Cache-Aside on Redis.
func LoadAndCacheUserPermissions(ctx context.Context, userID string) ([]string, error) {
	authzKey := rediskeys.PlatformUserAuthzKey(userID)

	if config.RedisClient != nil {
		exists, err := config.RedisClient.Exists(ctx, authzKey).Result()
		if err == nil && exists > 0 {
			members, err := config.RedisClient.SMembers(ctx, authzKey).Result()
			if err == nil {
				var perms []string
				for _, m := range members {
					if m != rediskeys.EmptyAuthzPlaceholder {
						perms = append(perms, m)
					}
				}
				return perms, nil
			}
		}
	}

	var permCodes []string
	err := config.DB.WithContext(ctx).Raw(`
		SELECT DISTINCT p.code
		FROM platform_permissions p
		JOIN platform_role_permissions rp ON rp.platform_permission_id = p.id
		JOIN platform_user_roles ur ON ur.platform_role_id = rp.platform_role_id
		WHERE ur.platform_user_id = ?
	`, userID).Scan(&permCodes).Error
	if err != nil {
		return nil, err
	}

	if config.RedisClient != nil {
		pipe := config.RedisClient.Pipeline()
		pipe.Del(ctx, authzKey)
		if len(permCodes) == 0 {
			pipe.SAdd(ctx, authzKey, rediskeys.EmptyAuthzPlaceholder)
		} else {
			members := make([]any, len(permCodes))
			for i, code := range permCodes {
				members[i] = code
			}
			pipe.SAdd(ctx, authzKey, members...)
		}
		pipe.Expire(ctx, authzKey, rediskeys.TTLPlatformAuthz)
		_, _ = pipe.Exec(ctx)
	}

	return permCodes, nil
}

func RequirePlatformPermission(permCode string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID, ok := c.Locals("platform_user_id").(string)
		if !ok || userID == "" {
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Chưa xác thực người dùng nền tảng", nil)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		authzKey := rediskeys.PlatformUserAuthzKey(userID)
		if config.RedisClient != nil {
			exists, err := config.RedisClient.Exists(ctx, authzKey).Result()
			if err == nil && exists > 0 {
				allowed, err := config.RedisClient.SIsMember(ctx, authzKey, permCode).Result()
				if err == nil {
					if !allowed {
						return response.Error(c, fiber.StatusForbidden, response.ErrCodePlatformPermissionDenied, "Bạn không có quyền thực hiện thao tác này", fiber.Map{
							"required_permission": permCode,
						})
					}
					return c.Next()
				}
			}
		}

		perms, err := LoadAndCacheUserPermissions(ctx, userID)
		if err != nil {
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Lỗi kiểm tra quyền hạn", nil)
		}

		hasPerm := false
		for _, p := range perms {
			if p == permCode {
				hasPerm = true
				break
			}
		}

		if !hasPerm {
			return response.Error(c, fiber.StatusForbidden, response.ErrCodePlatformPermissionDenied, "Bạn không có quyền thực hiện thao tác này", fiber.Map{
				"required_permission": permCode,
			})
		}

		return c.Next()
	}
}
