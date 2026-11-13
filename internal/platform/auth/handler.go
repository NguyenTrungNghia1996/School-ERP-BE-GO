package auth

import (
	"context"
	"errors"
	"school-erp/config"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/pkg/response"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Login(c *fiber.Ctx) error {
	var body LoginInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu đăng nhập không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.Email) == "" || body.Password == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Email và mật khẩu là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tokens, err := h.service.Login(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeInvalidCredentials, "Email hoặc mật khẩu không chính xác", nil)
		case errors.Is(err, ErrAccountLocked):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeAccountLocked, "Tài khoản đã bị khóa hoặc ngưng hoạt động", nil)
		case errors.Is(err, ErrMFARequired):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeMFARequired, "Vui lòng nhập mã xác thực 2 lớp (TOTP)", nil)
		case errors.Is(err, ErrMFAInvalid):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeMFAInvalid, "Mã xác thực 2 lớp (TOTP) không hợp lệ", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Lỗi xử lý đăng nhập", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, tokens)
}

func (h *Handler) Refresh(c *fiber.Ctx) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.RefreshToken) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "refresh_token là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tokens, err := h.service.Refresh(ctx, body.RefreshToken)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidTokenRealm):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeInvalidTokenRealm, "Token không thuộc realm quản trị nền tảng", nil)
		case errors.Is(err, ErrSessionExpired):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeSessionExpiredOrRevoked, "Phiên đăng nhập đã hết hạn hoặc bị thu hồi", nil)
		case errors.Is(err, ErrAccountLocked):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeAccountLocked, "Tài khoản đã bị khóa", nil)
		default:
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Refresh token không hợp lệ", nil)
		}
	}

	return response.Success(c, fiber.StatusOK, tokens)
}

func (h *Handler) Logout(c *fiber.Ctx) error {
	userID, _ := c.Locals("platform_user_id").(string)
	jti, _ := c.Locals("platform_jti").(string)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.service.Logout(ctx, userID, jti); err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể đăng xuất", err.Error())
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"logged_out": true})
}

func (h *Handler) Me(c *fiber.Ctx) error {
	userID, _ := c.Locals("platform_user_id").(string)
	if userID == "" {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Chưa xác thực", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	perms, err := loadUserPermissions(ctx, userID)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách quyền", err.Error())
	}

	profile, err := h.service.GetMe(ctx, userID, perms)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải thông tin hồ sơ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, profile)
}

func (h *Handler) SetupMFA(c *fiber.Ctx) error {
	userID, _ := c.Locals("platform_user_id").(string)
	if userID == "" {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Chưa xác thực", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := h.service.SetupMFA(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrMFAAlreadyEnabled) {
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Tài khoản đã bật xác thực 2 lớp (MFA). Vui lòng tắt MFA trước khi thiết lập lại", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể khởi tạo khóa MFA", err.Error())
	}

	return response.Success(c, fiber.StatusOK, res)
}

func (h *Handler) EnableMFA(c *fiber.Ctx) error {
	userID, _ := c.Locals("platform_user_id").(string)
	if userID == "" {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Chưa xác thực", nil)
	}

	var body struct {
		OTPCode string `json:"otp_code"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.OTPCode) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Vui lòng nhập mã xác thực 6 số (otp_code)", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.service.EnableMFA(ctx, userID, body.OTPCode, c.IP(), c.Get("User-Agent")); err != nil {
		switch {
		case errors.Is(err, ErrMFAAlreadyEnabled):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Xác thực 2 lớp (MFA) đã được kích hoạt", nil)
		case errors.Is(err, ErrMFANotSetup):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Chưa khởi tạo khóa MFA. Vui lòng gọi /me/mfa/setup trước", nil)
		case errors.Is(err, ErrMFAInvalid):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeMFAInvalid, "Mã xác thực 6 số (TOTP) không chính xác", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể kích hoạt MFA", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"mfa_enabled": true})
}

func (h *Handler) DisableMFA(c *fiber.Ctx) error {
	userID, _ := c.Locals("platform_user_id").(string)
	if userID == "" {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Chưa xác thực", nil)
	}

	var body struct {
		Password string `json:"password"`
		OTPCode  string `json:"otp_code"`
	}
	if err := c.BodyParser(&body); err != nil || body.Password == "" || strings.TrimSpace(body.OTPCode) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Mật khẩu hiện tại (password) và mã 6 số (otp_code) là bắt buộc để tắt MFA", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.service.DisableMFA(ctx, userID, body.Password, body.OTPCode, c.IP(), c.Get("User-Agent")); err != nil {
		switch {
		case errors.Is(err, ErrMFANotEnabled):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Tài khoản chưa bật xác thực 2 lớp (MFA)", nil)
		case errors.Is(err, ErrInvalidCredentials):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeInvalidCredentials, "Mật khẩu hiện tại không chính xác", nil)
		case errors.Is(err, ErrMFAInvalid):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeMFAInvalid, "Mã xác thực 6 số (TOTP) không chính xác", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tắt MFA", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"mfa_enabled": false})
}

func loadUserPermissions(ctx context.Context, userID string) ([]string, error) {
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
