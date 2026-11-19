package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"school-erp/config"
	"school-erp/internal/pkg/database"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/pkg/response"
	tenantauth "school-erp/internal/tenant/auth"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type cachedOrgSubdomain struct {
	Action         string `json:"action"`
	OrganizationID string `json:"organization_id"`
	Status         string `json:"status"`
}

type cachedSupportSession struct {
	SessionID      string   `json:"session_id"`
	ActorUserID    string   `json:"actor_user_id"`
	OrganizationID string   `json:"organization_id"`
	SchoolID       string   `json:"school_id"`
	SessionType    string   `json:"session_type"`
	Permissions    []string `json:"permissions"`
}

// TenantResolver xác minh chéo JWT (realm "tenant" hoặc "support") + X-Tenant-Subdomain + Membership/Support Session.
func TenantResolver() fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		tokenStr, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok || strings.TrimSpace(tokenStr) == "" {
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu Authorization Bearer token", nil)
		}

		claims, err := tenantauth.ParseTenantOrSupportToken(strings.TrimSpace(tokenStr))
		if err != nil {
			if errors.Is(err, tenantauth.ErrInvalidTokenRealm) {
				return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeInvalidTokenRealm, "Token không thuộc realm đơn vị/trường học hoặc support mode", nil)
			}
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Token không hợp lệ hoặc đã hết hạn", nil)
		}
		if claims.TokenType != "" && claims.TokenType != "access" {
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Yêu cầu sử dụng Access Token", nil)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// 1. Đọc & phân giải X-Tenant-Subdomain -> organization_id (Redis 30m -> DB)
		subdomain := strings.ToLower(strings.TrimSpace(c.Get("X-Tenant-Subdomain")))
		if subdomain == "" {
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Thiếu header X-Tenant-Subdomain", nil)
		}

		orgID, orgStatus, err := resolveOrganizationBySubdomain(ctx, subdomain)
		if err != nil || orgID == "" {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Đơn vị cho subdomain này", nil)
		}
		if orgStatus != "ACTIVE" {
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeTenantAccessDenied, "Đơn vị hiện không ở trạng thái hoạt động (ACTIVE)", nil)
		}

		// 2. Xác minh phiên & đối chiếu chéo Membership / Support Session
		var schoolID string
		var membershipID string

		if claims.Realm == "support" {
			sess, err := verifySupportSession(ctx, claims.JTI, orgID)
			if err != nil {
				return response.Error(c, fiber.StatusForbidden, response.ErrCodeTenantAccessDenied, err.Error(), nil)
			}
			sessionType := strings.ToUpper(sess.SessionType)
			if sessionType == "" {
				sessionType = strings.ToUpper(claims.SessionType)
			}
			if sessionType == "READ_ONLY" && !isReadOnlyMethod(c.Method()) {
				return response.Error(c, fiber.StatusForbidden, response.ErrCodeSupportReadOnly, "Phiên hỗ trợ đang ở chế độ chỉ đọc (READ_ONLY)", nil)
			}

			schoolID = strings.TrimSpace(c.Get("X-School-ID"))
			if schoolID == "" {
				schoolID = sess.SchoolID
			}
			if schoolID == "" {
				schoolID = claims.SchoolID
			}
			if schoolID != "" && !schoolBelongsToOrg(ctx, orgID, schoolID) {
				return response.Error(c, fiber.StatusForbidden, response.ErrCodeTenantAccessDenied, "Trường học không thuộc Đơn vị hiện tại", nil)
			}

			c.Locals("tenant_is_support_mode", true)
			c.Locals("support_session_id", claims.JTI)
			c.Locals("support_session_type", sessionType)
			c.Locals("support_permissions", sess.Permissions)
		} else {
			// Kiểm tra blacklist & session Redis cho tenant token
			if config.RedisClient != nil && claims.JTI != "" {
				if bl, err := config.RedisClient.Exists(ctx, rediskeys.TenantBlacklistJTIKey(claims.JTI)).Result(); err == nil && bl > 0 {
					return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeTokenRevoked, "Phiên đăng nhập đã bị thu hồi", nil)
				}
				if ex, err := config.RedisClient.Exists(ctx, rediskeys.TenantSessionKey(claims.JTI)).Result(); err == nil && ex == 0 {
					return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeSessionExpiredOrRevoked, "Phiên đăng nhập đã hết hạn hoặc bị thu hồi", nil)
				}
			}

			if !userBelongsToOrg(ctx, claims.UserID, orgID) {
				return response.Error(c, fiber.StatusForbidden, response.ErrCodeTenantAccessDenied, "Tài khoản không thuộc Đơn vị này", nil)
			}

			requestedSchoolID := strings.TrimSpace(c.Get("X-School-ID"))
			if requestedSchoolID == "" {
				requestedSchoolID = claims.SchoolID
			}

			mID, resolvedSchoolID, err := resolveActiveMembership(ctx, claims.UserID, orgID, requestedSchoolID)
			if err != nil {
				return response.Error(c, fiber.StatusForbidden, response.ErrCodeTenantAccessDenied, "Không tìm thấy membership hợp lệ tại trường học này", nil)
			}
			membershipID = mID
			schoolID = resolvedSchoolID
			c.Locals("tenant_is_support_mode", false)
		}

		c.Locals("tenant_ctx", database.TenantContext{
			OrganizationID: orgID,
			SchoolID:       schoolID,
		})
		c.Locals("tenant_user_id", claims.UserID)
		c.Locals("tenant_org_id", orgID)
		c.Locals("tenant_school_id", schoolID)
		c.Locals("tenant_membership_id", membershipID)
		c.Locals("tenant_subdomain", subdomain)
		c.Locals("tenant_realm", claims.Realm)
		c.Locals("tenant_jti", claims.JTI)

		return c.Next()
	}
}

func isReadOnlyMethod(method string) bool {
	return method == fiber.MethodGet || method == fiber.MethodHead || method == fiber.MethodOptions
}

func resolveOrganizationBySubdomain(ctx context.Context, subdomain string) (string, string, error) {
	cacheKey := rediskeys.TenantSubdomainKey(subdomain)
	if config.RedisClient != nil {
		if raw, err := config.RedisClient.Get(ctx, cacheKey).Result(); err == nil && raw != "" {
			var cached cachedOrgSubdomain
			if json.Unmarshal([]byte(raw), &cached) == nil && cached.OrganizationID != "" {
				status := cached.Status
				if status == "" {
					status = cached.Action
				}
				return cached.OrganizationID, status, nil
			}
		}
	}

	var row struct {
		ID     uuid.UUID `gorm:"column:id"`
		Status string    `gorm:"column:status"`
	}
	err := config.DB.WithContext(ctx).
		Table("organizations").
		Select("id, status").
		Where("LOWER(subdomain) = ? AND deleted_at IS NULL", subdomain).
		First(&row).Error
	if err != nil {
		return "", "", err
	}

	if config.RedisClient != nil {
		payload, _ := json.Marshal(cachedOrgSubdomain{
			Action:         row.Status,
			OrganizationID: row.ID.String(),
			Status:         row.Status,
		})
		_ = config.RedisClient.Set(ctx, cacheKey, string(payload), rediskeys.TTLTenantSubdomain).Err()
	}
	return row.ID.String(), row.Status, nil
}

func userBelongsToOrg(ctx context.Context, userID, orgID string) bool {
	cacheKey := rediskeys.TenantUserOrgsKey(userID)
	if config.RedisClient != nil {
		if ex, err := config.RedisClient.Exists(ctx, cacheKey).Result(); err == nil && ex > 0 {
			ok, err := config.RedisClient.SIsMember(ctx, cacheKey, orgID).Result()
			if err == nil {
				return ok
			}
		}
	}

	var orgIDs []string
	if err := config.DB.WithContext(ctx).
		Table("school_memberships").
		Where("user_id = ? AND status = 'ACTIVE'", userID).
		Distinct("organization_id").
		Pluck("organization_id", &orgIDs).Error; err != nil {
		return false
	}

	if config.RedisClient != nil {
		pipe := config.RedisClient.Pipeline()
		pipe.Del(ctx, cacheKey)
		if len(orgIDs) == 0 {
			pipe.SAdd(ctx, cacheKey, rediskeys.EmptyAuthzPlaceholder)
		} else {
			members := make([]any, len(orgIDs))
			for i, id := range orgIDs {
				members[i] = id
			}
			pipe.SAdd(ctx, cacheKey, members...)
		}
		pipe.Expire(ctx, cacheKey, rediskeys.TTLTenantUserOrgs)
		_, _ = pipe.Exec(ctx)
	}

	for _, id := range orgIDs {
		if id == orgID {
			return true
		}
	}
	return false
}

func resolveActiveMembership(ctx context.Context, userID, orgID, requestedSchoolID string) (string, string, error) {
	var row struct {
		ID       uuid.UUID `gorm:"column:id"`
		SchoolID uuid.UUID `gorm:"column:school_id"`
	}

	q := config.DB.WithContext(ctx).
		Table("school_memberships").
		Select("id, school_id").
		Where("user_id = ? AND organization_id = ? AND status = 'ACTIVE'", userID, orgID)

	if requestedSchoolID != "" {
		if _, err := uuid.Parse(requestedSchoolID); err != nil {
			return "", "", errors.New("invalid school_id")
		}
		q = q.Where("school_id = ?", requestedSchoolID)
	} else {
		q = q.Order("created_at ASC")
	}

	if err := q.First(&row).Error; err != nil {
		return "", "", err
	}
	return row.ID.String(), row.SchoolID.String(), nil
}

func schoolBelongsToOrg(ctx context.Context, orgID, schoolID string) bool {
	if _, err := uuid.Parse(schoolID); err != nil {
		return false
	}
	var count int64
	config.DB.WithContext(ctx).
		Table("schools").
		Where("id = ? AND organization_id = ? AND deleted_at IS NULL", schoolID, orgID).
		Count(&count)
	return count > 0
}

func verifySupportSession(ctx context.Context, sessionID, expectedOrgID string) (*cachedSupportSession, error) {
	if sessionID == "" {
		return nil, errors.New("Phiên hỗ trợ không hợp lệ")
	}

	if config.RedisClient != nil {
		raw, err := config.RedisClient.Get(ctx, rediskeys.SupportSessionKey(sessionID)).Result()
		if err == nil && raw != "" {
			var sess cachedSupportSession
			if json.Unmarshal([]byte(raw), &sess) == nil {
				if sess.OrganizationID != expectedOrgID {
					return nil, errors.New("Phiên hỗ trợ không khớp Đơn vị đang truy cập")
				}
				return &sess, nil
			}
		}
	}

	// Fallback DB check
	var row struct {
		ID                 uuid.UUID  `gorm:"column:id"`
		ActorUserID        uuid.UUID  `gorm:"column:actor_user_id"`
		OrganizationID     uuid.UUID  `gorm:"column:organization_id"`
		SchoolID           *uuid.UUID `gorm:"column:school_id"`
		SessionType        string     `gorm:"column:session_type"`
		GrantedPermissions []byte     `gorm:"column:granted_permissions"`
	}
	now := time.Now()
	err := config.DB.WithContext(ctx).
		Table("support_access_sessions").
		Where("id = ? AND organization_id = ? AND status = 'ACTIVE' AND revoked_at IS NULL AND expires_at > ?", sessionID, expectedOrgID, now).
		First(&row).Error
	if err != nil {
		return nil, errors.New("Phiên hỗ trợ đã hết hạn, bị thu hồi hoặc không thuộc Đơn vị này")
	}

	var perms []string
	_ = json.Unmarshal(row.GrantedPermissions, &perms)
	sID := ""
	if row.SchoolID != nil {
		sID = row.SchoolID.String()
	}
	return &cachedSupportSession{
		SessionID:      row.ID.String(),
		ActorUserID:    row.ActorUserID.String(),
		OrganizationID: row.OrganizationID.String(),
		SchoolID:       sID,
		SessionType:    row.SessionType,
		Permissions:    perms,
	}, nil
}
