package menu

import (
	"context"
	"errors"
	"school-erp/internal/pkg/database"
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

func getTenantActorUUID(c *fiber.Ctx) uuid.UUID {
	if s, ok := c.Locals("tenant_user_id").(string); ok {
		if id, err := uuid.Parse(s); err == nil {
			return id
		}
	}
	return uuid.Nil
}

// GetManageTree godoc
// @Summary Lấy cây menu quản trị của Đơn vị (Menu nền + Tùy chỉnh)
// @Tags Tenant Menu
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string true "Subdomain của Đơn vị"
// @Success 200 {object} response.SuccessResponse{data=[]DynamicMenuNode}
// @Failure 401 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/menus/manage [get]
func (h *Handler) GetManageTree(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	orgID, err := uuid.Parse(tc.OrganizationID)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tree, err := h.service.GetManageMenuTree(ctx, orgID)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải cây menu đơn vị", err.Error())
	}
	return response.Success(c, fiber.StatusOK, tree)
}

// CustomizeMenus godoc
// @Summary Tùy chỉnh nhãn, icon, thứ tự và ẩn/hiện menu của Đơn vị
// @Tags Tenant Menu
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string true "Subdomain của Đơn vị"
// @Param body body CustomizeMenusInput true "Danh sách cấu hình menu"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/menus/customize [put]
func (h *Handler) CustomizeMenus(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	var body CustomizeMenusInput
	if err := c.BodyParser(&body); err != nil || len(body.Items) == 0 {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Danh sách items cấu hình menu là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.service.CustomizeMenus(ctx, tc, body.Items, getTenantActorUUID(c), c.IP(), c.Get("User-Agent")); err != nil {
		if errors.Is(err, ErrMandatoryMenuCannotHide) {
			return response.Error(c, fiber.StatusForbidden, response.ErrCodePermissionDenied, "Không được phép ẩn menu bắt buộc của hệ thống (is_mandatory = true)", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật cấu hình menu", err.Error())
	}
	return response.Success(c, fiber.StatusOK, fiber.Map{"updated": len(body.Items)})
}

// CreateCustomItem godoc
// @Summary Tạo lối tắt menu riêng trỏ tới route hợp lệ thuộc gói của Đơn vị
// @Tags Tenant Menu
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string true "Subdomain của Đơn vị"
// @Param body body CreateCustomMenuInput true "Thông tin menu riêng"
// @Success 201 {object} response.SuccessResponse{data=OrganizationMenuConfig}
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/menus/custom-items [post]
func (h *Handler) CreateCustomItem(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	var body CreateCustomMenuInput
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.CustomLabel) == "" || body.RouteID == uuid.Nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "custom_label và route_id là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := h.service.CreateCustomMenuItem(ctx, tc, body, getTenantActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrRouteNotFoundOrDenied) {
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeFeatureNotSubscribed, "Route không tồn tại hoặc không thuộc gói tính năng của Đơn vị", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo menu riêng", err.Error())
	}
	return response.Success(c, fiber.StatusCreated, cfg)
}

// ListEntitledRoutes godoc
// @Summary Danh sách Route khả dụng theo gói của Đơn vị (dùng cho dropdown tạo menu riêng)
// @Tags Tenant Menu
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string true "Subdomain của Đơn vị"
// @Success 200 {object} response.SuccessResponse{data=[]EntitledRouteItem}
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/menus/routes [get]
func (h *Handler) ListEntitledRoutes(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	orgID, err := uuid.Parse(tc.OrganizationID)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	routes, err := h.service.ListEntitledRoutes(ctx, orgID)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách route khả dụng", err.Error())
	}
	return response.Success(c, fiber.StatusOK, routes)
}

// DeleteCustomItem godoc
// @Summary Xóa lối tắt menu riêng của Đơn vị
// @Tags Tenant Menu
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string true "Subdomain của Đơn vị"
// @Param id path string true "Custom Menu Config ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/menus/custom-items/{id} [delete]
func (h *Handler) DeleteCustomItem(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	configID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID menu riêng không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.service.DeleteCustomMenuItem(ctx, tc, configID, getTenantActorUUID(c), c.IP(), c.Get("User-Agent")); err != nil {
		if errors.Is(err, ErrCustomMenuNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy mục menu riêng", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa mục menu riêng", err.Error())
	}
	return response.Success(c, fiber.StatusOK, fiber.Map{"deleted_id": configID})
}
