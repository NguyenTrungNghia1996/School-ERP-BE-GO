package middleware

import (
	"context"
	"errors"
	"school-erp/config"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/pkg/response"
	"school-erp/internal/platform/auth"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

func PlatformAuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu hoặc sai định dạng Authorization Bearer token", nil)
		}

		tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		claims, err := auth.ParsePlatformToken(tokenStr)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidTokenRealm) {
				return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeInvalidTokenRealm, "Token không thuộc realm quản trị nền tảng", nil)
			}
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Token không hợp lệ hoặc đã hết hạn", nil)
		}

		if claims.TokenType != "" && claims.TokenType != "access" {
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Loại token không hợp lệ cho truy cập API", nil)
		}

		if config.RedisClient != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			// 1. Kiểm tra blacklist JTI
			blacklisted, err := config.RedisClient.Exists(ctx, rediskeys.PlatformBlacklistJTIKey(claims.JTI)).Result()
			if err == nil && blacklisted > 0 {
				return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeSessionExpiredOrRevoked, "Phiên đăng nhập đã bị thu hồi (TOKEN_REVOKED)", nil)
			}

			// 2. Kiểm tra session tồn tại trên Redis
			sessionExists, err := config.RedisClient.Exists(ctx, rediskeys.PlatformSessionKey(claims.JTI)).Result()
			if err == nil && sessionExists == 0 {
				return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeSessionExpiredOrRevoked, "Phiên đăng nhập đã hết hạn hoặc đã đăng xuất (SESSION_EXPIRED)", nil)
			}
		}

		c.Locals("platform_user_id", claims.UserID)
		c.Locals("platform_jti", claims.JTI)
		return c.Next()
	}
}
