package iam

import (
	"context"
	"errors"
	"school-erp/internal/pkg/response"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func getActorUUID(c *fiber.Ctx) uuid.UUID {
	if s, ok := c.Locals("platform_user_id").(string); ok {
		if id, err := uuid.Parse(s); err == nil {
			return id
		}
	}
	return uuid.Nil
}

// ListUsers godoc
// @Summary Danh sách nhân sự nền tảng
// @Tags Platform IAM
// @Produce json
// @Security BearerAuth
// @Param search query string false "Tìm kiếm"
// @Param status query string false "Trạng thái (ACTIVE, LOCKED, INACTIVE)"
// @Param role_id query string false "Lọc theo Role ID"
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} map[string]any
// @Router /v1/platform/users [get]
func (h *Handler) ListUsers(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	search := c.Query("search", c.Query("q"))

	users, total, err := h.service.ListUsers(ctx, ListUsersFilter{
		Search: search,
		Status: c.Query("status"),
		RoleID: c.Query("role_id"),
		Page:   page,
		Limit:  limit,
	})
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách nhân sự nền tảng", err.Error())
	}

	return response.SuccessWithMeta(c, fiber.StatusOK, users, response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// CreateUser godoc
// @Summary Tạo mới nhân sự nền tảng
// @Tags Platform IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateUserInput true "Thông tin nhân sự"
// @Success 201 {object} map[string]any
// @Router /v1/platform/users [post]
func (h *Handler) CreateUser(c *fiber.Ctx) error {
	var body CreateUserInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu đầu vào không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.Email) == "" || strings.TrimSpace(body.Password) == "" || strings.TrimSpace(body.FullName) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Email, mật khẩu và họ tên là bắt buộc", nil)
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	user, err := h.service.CreateUser(ctx, body)
	if err != nil {
		if errors.Is(err, ErrEmailTaken) {
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Email đã tồn tại trên hệ thống", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo nhân sự nền tảng", err.Error())
	}

	return response.Success(c, fiber.StatusCreated, user)
}

// UpdateUserRoles godoc
// @Summary Cập nhật vai trò nhân sự nền tảng
// @Tags Platform IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Param body body map[string][]string true "role_ids"
// @Success 200 {object} map[string]any
// @Router /v1/platform/users/{id}/roles [put]
func (h *Handler) UpdateUserRoles(c *fiber.Ctx) error {
	targetID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID người dùng không hợp lệ", nil)
	}

	var body struct {
		RoleIDs []uuid.UUID `json:"role_ids"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu role_ids không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h.service.UpdateUserRoles(ctx, targetID, body.RoleIDs, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy người dùng", nil)
		case errors.Is(err, ErrLastRootAdmin):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodeLastRootAdminProtected, "Không thể gỡ vai trò ROOT_ADMIN khỏi quản trị viên tối cao cuối cùng", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật vai trò người dùng", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"user_id": targetID, "role_ids": body.RoleIDs})
}

// UpdateUserStatus godoc
// @Summary Cập nhật trạng thái nhân sự nền tảng
// @Tags Platform IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Param body body map[string]string true "status, reason"
// @Success 200 {object} map[string]any
// @Router /v1/platform/users/{id}/status [patch]
func (h *Handler) UpdateUserStatus(c *fiber.Ctx) error {
	targetID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID người dùng không hợp lệ", nil)
	}

	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	status := strings.ToUpper(strings.TrimSpace(body.Status))
	if status != "ACTIVE" && status != "LOCKED" && status != "INACTIVE" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Trạng thái phải là ACTIVE, LOCKED hoặc INACTIVE", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h.service.UpdateUserStatus(ctx, targetID, status, body.Reason, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy người dùng", nil)
		case errors.Is(err, ErrSelfLockNotAllowed):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodeSelfLockNotAllowed, "Không thể tự khóa tài khoản của chính mình", nil)
		case errors.Is(err, ErrLastRootAdmin):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodeLastRootAdminProtected, "Không thể khóa tài khoản ROOT_ADMIN cuối cùng trên hệ thống", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật trạng thái nhân sự", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"user_id": targetID, "status": status})
}

// ListRoles godoc
// @Summary Danh sách vai trò nền tảng
// @Tags Platform IAM
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Router /v1/platform/roles [get]
func (h *Handler) ListRoles(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	roles, err := h.service.ListRoles(ctx)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách vai trò", err.Error())
	}

	return response.Success(c, fiber.StatusOK, roles)
}

// ListPermissions godoc
// @Summary Danh sách quyền nền tảng
// @Tags Platform IAM
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Router /v1/platform/permissions [get]
func (h *Handler) ListPermissions(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	perms, err := h.service.ListPermissions(ctx)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách quyền nền tảng", err.Error())
	}

	return response.Success(c, fiber.StatusOK, perms)
}

// CreateRole godoc
// @Summary Tạo mới vai trò nền tảng
// @Tags Platform IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateRoleInput true "Thông tin vai trò"
// @Success 201 {object} map[string]any
// @Router /v1/platform/roles [post]
func (h *Handler) CreateRole(c *fiber.Ctx) error {
	var body CreateRoleInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu đầu vào không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.Code) == "" || strings.TrimSpace(body.Name) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Mã vai trò (code) và tên vai trò (name) là bắt buộc", nil)
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	role, err := h.service.CreateRole(ctx, body)
	if err != nil {
		if errors.Is(err, ErrRoleCodeTaken) {
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Mã vai trò đã tồn tại", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo vai trò mới", err.Error())
	}

	return response.Success(c, fiber.StatusCreated, role)
}

// UpdateRolePermissions godoc
// @Summary Cập nhật quyền cho vai trò nền tảng
// @Tags Platform IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Role ID"
// @Param body body map[string][]string true "permission_ids / permission_codes"
// @Success 200 {object} map[string]any
// @Router /v1/platform/roles/{id}/permissions [put]
func (h *Handler) UpdateRolePermissions(c *fiber.Ctx) error {
	roleID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID vai trò không hợp lệ", nil)
	}

	var body struct {
		PermissionIDs   []uuid.UUID `json:"permission_ids"`
		PermissionCodes []string    `json:"permission_codes"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h.service.UpdateRolePermissions(ctx, roleID, body.PermissionIDs, body.PermissionCodes, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrRoleNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy vai trò", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật quyền cho vai trò", err.Error())
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{
		"role_id":          roleID,
		"permission_ids":   body.PermissionIDs,
		"permission_codes": body.PermissionCodes,
	})
}

// ResetUserPassword godoc
// @Summary Đặt lại mật khẩu nhân sự nền tảng
// @Tags Platform IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Param body body map[string]any true "new_password, must_change_password, reason"
// @Success 200 {object} map[string]any
// @Router /v1/platform/users/{id}/password [patch]
func (h *Handler) ResetUserPassword(c *fiber.Ctx) error {
	targetID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID người dùng không hợp lệ", nil)
	}

	var body struct {
		NewPassword        string `json:"new_password"`
		MustChangePassword *bool  `json:"must_change_password"`
		Reason             string `json:"reason"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.NewPassword) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "new_password là bắt buộc", nil)
	}

	mustChange := true
	if body.MustChangePassword != nil {
		mustChange = *body.MustChangePassword
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h.service.ResetUserPassword(ctx, targetID, body.NewPassword, mustChange, strings.TrimSpace(body.Reason), getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrWeakPassword):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Mật khẩu mới phải có tối thiểu 8 ký tự", nil)
		case errors.Is(err, ErrUserNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy người dùng", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể đặt lại mật khẩu nhân sự", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{
		"user_id":              targetID,
		"must_change_password": mustChange,
	})
}

// ForceDisableUserMFA godoc
// @Summary Tắt bắt buộc MFA của nhân sự nền tảng
// @Tags Platform IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Param body body map[string]string true "reason"
// @Success 200 {object} map[string]any
// @Router /v1/platform/users/{id}/mfa [delete]
// @Router /v1/platform/users/{id}/mfa/disable [patch]
func (h *Handler) ForceDisableUserMFA(c *fiber.Ctx) error {
	targetID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID người dùng không hợp lệ", nil)
	}

	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.BodyParser(&body)
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Bắt buộc nhập lý do (reason) khi tắt MFA của thành viên (VD: Mất thiết bị xác thực)", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h.service.ForceDisableUserMFA(ctx, targetID, reason, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy người dùng", nil)
		case errors.Is(err, ErrMFANotEnabled):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Thành viên này hiện chưa bật xác thực 2 lớp (MFA)", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tắt MFA cho thành viên", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{
		"user_id":     targetID,
		"mfa_enabled": false,
	})
}

