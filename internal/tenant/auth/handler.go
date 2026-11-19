package auth

import (
	"context"
	"errors"
	"school-erp/internal/pkg/database"
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

// Login godoc
// @Summary Đăng nhập người dùng Đơn vị / Trường học (Tenant)
// @Tags Tenant Auth
// @Accept json
// @Produce json
// @Param body body TenantLoginInput true "subdomain, identifier (email/username/phone), password"
// @Success 200 {object} response.SuccessResponse{data=TenantLoginOutput}
// @Failure 400 {object} response.ErrorResponse
// @Failure 401 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/auth/login [post]
func (h *Handler) Login(c *fiber.Ctx) error {
	var body TenantLoginInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu đăng nhập không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.Subdomain) == "" {
		body.Subdomain = strings.TrimSpace(c.Get("X-Tenant-Subdomain"))
	}
	if strings.TrimSpace(body.Subdomain) == "" || strings.TrimSpace(body.Identifier) == "" || body.Password == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "subdomain, identifier và password là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := h.service.Login(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrOrgNotFoundOrInactive):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Đơn vị hoặc Đơn vị chưa được kích hoạt", nil)
		case errors.Is(err, ErrInvalidCredentials):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeInvalidCredentials, "Tài khoản hoặc mật khẩu không chính xác", nil)
		case errors.Is(err, ErrAccountLocked):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeAccountLocked, "Tài khoản đã bị khóa hoặc ngưng hoạt động", nil)
		case errors.Is(err, ErrNoMembershipInOrg):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeTenantAccessDenied, "Tài khoản không thuộc Đơn vị này", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Lỗi xử lý đăng nhập", err.Error())
		}
	}
	return response.Success(c, fiber.StatusOK, out)
}

// Refresh godoc
// @Summary Làm mới Access Token Tenant
// @Tags Tenant Auth
// @Accept json
// @Produce json
// @Param body body TenantRefreshInput true "refresh_token"
// @Success 200 {object} response.SuccessResponse{data=TenantLoginOutput}
// @Failure 400 {object} response.ErrorResponse
// @Failure 401 {object} response.ErrorResponse
// @Router /v1/auth/refresh [post]
func (h *Handler) Refresh(c *fiber.Ctx) error {
	var body TenantRefreshInput
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.RefreshToken) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "refresh_token là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := h.service.Refresh(ctx, body.RefreshToken)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidTokenRealm):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeInvalidTokenRealm, "Token không thuộc realm đơn vị", nil)
		case errors.Is(err, ErrSessionExpired):
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeSessionExpiredOrRevoked, "Phiên đăng nhập đã hết hạn hoặc bị thu hồi", nil)
		case errors.Is(err, ErrAccountLocked):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeAccountLocked, "Tài khoản đã bị khóa", nil)
		default:
			return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Refresh token không hợp lệ", nil)
		}
	}
	return response.Success(c, fiber.StatusOK, out)
}

// Me godoc
// @Summary Lấy thông tin ngữ cảnh người dùng Đơn vị kèm cây Menu động đã lọc
// @Tags Tenant Auth
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param X-School-ID header string false "School ID đang chọn"
// @Success 200 {object} response.SuccessResponse{data=TenantMeOutput}
// @Failure 401 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/me [get]
func (h *Handler) Me(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	userID, _ := c.Locals("tenant_user_id").(string)
	membershipID, _ := c.Locals("tenant_membership_id").(string)
	isSupport, _ := c.Locals("tenant_is_support_mode").(bool)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := h.service.GetMe(ctx, tc, userID, membershipID, isSupport)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải thông tin người dùng", err.Error())
	}
	return response.Success(c, fiber.StatusOK, out)
}

// SwitchSchool godoc
// @Summary Chuyển đổi trường học đang thao tác trong cùng 1 Đơn vị
// @Tags Tenant Auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param body body SwitchSchoolInput true "school_id"
// @Success 200 {object} response.SuccessResponse{data=TenantLoginOutput}
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/auth/switch-school [post]
func (h *Handler) SwitchSchool(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	userID, _ := c.Locals("tenant_user_id").(string)
	jti, _ := c.Locals("tenant_jti").(string)

	var body SwitchSchoolInput
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.SchoolID) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "school_id là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := h.service.SwitchSchool(ctx, tc, userID, body.SchoolID, jti)
	if err != nil {
		if errors.Is(err, ErrSchoolAccessDenied) {
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeTenantAccessDenied, "Bạn không có quyền truy cập vào trường học này", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể chuyển đổi trường học", err.Error())
	}
	return response.Success(c, fiber.StatusOK, out)
}

// Logout godoc
// @Summary Đăng xuất phiên người dùng Đơn vị
// @Tags Tenant Auth
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Success 200 {object} response.SuccessResponse
// @Router /v1/auth/logout [post]
func (h *Handler) Logout(c *fiber.Ctx) error {
	userID, _ := c.Locals("tenant_user_id").(string)
	jti, _ := c.Locals("tenant_jti").(string)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.service.Logout(ctx, userID, jti); err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể đăng xuất", err.Error())
	}
	return response.Success(c, fiber.StatusOK, fiber.Map{"logged_out": true})
}
