package catalog

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

// ListFeatures godoc
// @Summary Danh sách tính năng (Feature Catalog)
// @Tags Platform Catalog
// @Produce json
// @Security BearerAuth
// @Param module query string false "Module"
// @Param is_active query string false "Trạng thái kích hoạt"
// @Param search query string false "Tìm kiếm"
// @Success 200 {object} response.SuccessResponse{data=[]Feature}
// @Failure 401 {object} response.ErrorResponse
// @Router /v1/platform/catalog/features [get]
func (h *Handler) ListFeatures(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	features, err := h.service.ListFeatures(ctx, ListFeaturesFilter{
		Module:   c.Query("module"),
		IsActive: c.Query("is_active"),
		Search:   c.Query("search", c.Query("q")),
	})
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách tính năng", err.Error())
	}

	return response.Success(c, fiber.StatusOK, features)
}

// CreateFeature godoc
// @Summary Tạo mới tính năng trong Catalog
// @Tags Platform Catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateFeatureInput true "Thông tin tính năng"
// @Success 201 {object} response.SuccessResponse{data=Feature}
// @Failure 400 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/catalog/features [post]
func (h *Handler) CreateFeature(c *fiber.Ctx) error {
	var body CreateFeatureInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.Code) == "" || strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Module) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "code, name và module là bắt buộc", nil)
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	feature, err := h.service.CreateFeature(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidFeatureCodeFormat):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Mã tính năng (code) phải viết hoa theo chuẩn ^[A-Z0-9_]+$", nil)
		case errors.Is(err, ErrCodeAlreadyExists):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Mã tính năng (code) đã tồn tại", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo tính năng", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, feature)
}

// UpdateFeature godoc
// @Summary Cập nhật tính năng trong Catalog
// @Tags Platform Catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Feature ID"
// @Param body body UpdateFeatureInput true "Thông tin cập nhật"
// @Success 200 {object} response.SuccessResponse{data=Feature}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 422 {object} response.ErrorResponse
// @Router /v1/platform/catalog/features/{id} [put]
func (h *Handler) UpdateFeature(c *fiber.Ctx) error {
	featureID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tính năng không hợp lệ", nil)
	}

	var body UpdateFeatureInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	feature, err := h.service.UpdateFeature(ctx, featureID, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrFeatureNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tính năng", nil)
		case errors.Is(err, ErrFeatureInUseCannotModify):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodeFeatureInUseCannotModify, "Không được sửa mã (code) của tính năng đã được gắn vào gói dịch vụ hoặc quyền hạn", nil)
		case errors.Is(err, ErrInvalidFeatureCodeFormat):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Mã tính năng (code) phải theo chuẩn ^[A-Z0-9_]+$", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật tính năng", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, feature)
}

// ListPermissions godoc
// @Summary Danh sách quyền Tenant (Permission Catalog)
// @Tags Platform Catalog
// @Produce json
// @Security BearerAuth
// @Param feature_id query string false "Feature ID"
// @Param resource query string false "Resource"
// @Param is_sensitive query string false "Quyền nhạy cảm"
// @Success 200 {object} response.SuccessResponse{data=[]TenantPermission}
// @Failure 401 {object} response.ErrorResponse
// @Router /v1/platform/catalog/permissions [get]
func (h *Handler) ListPermissions(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	perms, err := h.service.ListPermissions(ctx, ListPermissionsFilter{
		FeatureID:   c.Query("feature_id"),
		Resource:    c.Query("resource"),
		IsSensitive: c.Query("is_sensitive"),
	})
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách quyền Tenant", err.Error())
	}

	return response.Success(c, fiber.StatusOK, perms)
}

// CreatePermission godoc
// @Summary Tạo mới quyền Tenant trong Catalog
// @Tags Platform Catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreatePermissionInput true "Thông tin quyền"
// @Success 201 {object} response.SuccessResponse{data=TenantPermission}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/catalog/permissions [post]
func (h *Handler) CreatePermission(c *fiber.Ctx) error {
	var body CreatePermissionInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}
	if body.FeatureID == uuid.Nil || strings.TrimSpace(body.Code) == "" || strings.TrimSpace(body.Name) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "feature_id, code và name là bắt buộc", nil)
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	perm, err := h.service.CreatePermission(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrFeatureNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tính năng (feature_id)", nil)
		case errors.Is(err, ErrInvalidPermCodeFormat):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Mã quyền (code) phải theo chuẩn resource:action (^[a-z0-9_]+:[a-z0-9_]+$)", nil)
		case errors.Is(err, ErrCodeAlreadyExists):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Mã quyền (code) đã tồn tại", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo quyền Tenant", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, perm)
}

// UpdatePermission godoc
// @Summary Cập nhật quyền Tenant trong Catalog
// @Tags Platform Catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Permission ID"
// @Param body body UpdatePermissionInput true "Thông tin cập nhật"
// @Success 200 {object} response.SuccessResponse{data=TenantPermission}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 422 {object} response.ErrorResponse
// @Router /v1/platform/catalog/permissions/{id} [put]
func (h *Handler) UpdatePermission(c *fiber.Ctx) error {
	permID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID quyền không hợp lệ", nil)
	}

	var body UpdatePermissionInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	perm, err := h.service.UpdatePermission(ctx, permID, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrPermissionNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy quyền", nil)
		case errors.Is(err, ErrFeatureInUseCannotModify):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodeFeatureInUseCannotModify, "Không được phép thay đổi mã quyền (code)", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật quyền", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, perm)
}

// ListRoutes godoc
// @Summary Danh sách Route của Tenant
// @Tags Platform Catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.SuccessResponse{data=[]TenantRoute}
// @Failure 401 {object} response.ErrorResponse
// @Router /v1/platform/catalog/routes [get]
func (h *Handler) ListRoutes(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	routes, err := h.service.ListRoutes(ctx)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách route Tenant", err.Error())
	}

	return response.Success(c, fiber.StatusOK, routes)
}

// GetMenuTree godoc
// @Summary Cây menu nền của Tenant (Base Menu Tree)
// @Tags Platform Catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.SuccessResponse{data=[]TenantMenuTreeNode}
// @Failure 401 {object} response.ErrorResponse
// @Router /v1/platform/catalog/menus/tree [get]
func (h *Handler) GetMenuTree(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tree, err := h.service.GetTenantBaseMenuTree(ctx)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy cây menu nền Tenant", err.Error())
	}

	return response.Success(c, fiber.StatusOK, tree)
}

// CreateMenu godoc
// @Summary Tạo node mới trên cây menu nền của Tenant
// @Tags Platform Catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateMenuInput true "Thông tin node menu"
// @Success 201 {object} response.SuccessResponse{data=TenantBaseMenu}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/catalog/menus [post]
func (h *Handler) CreateMenu(c *fiber.Ctx) error {
	var body CreateMenuInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.MenuKey) == "" || strings.TrimSpace(body.Label) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "menu_key và label là bắt buộc", nil)
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	node, err := h.service.CreateMenuNode(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrItemMenuRequiresRoute):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Menu kiểu ITEM bắt buộc phải chọn route_id hợp lệ trong tenant_routes", nil)
		case errors.Is(err, ErrRouteNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy route_id trong tenant_routes", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo node menu", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, node)
}

// ReorderMenus godoc
// @Summary Sắp xếp lại cây menu nền của Tenant
// @Tags Platform Catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body []ReorderMenuItem true "Danh sách thứ tự menu"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Router /v1/platform/catalog/menus/reorder [put]
func (h *Handler) ReorderMenus(c *fiber.Ctx) error {
	var body struct {
		Items []ReorderMenuItem `json:"items"`
	}
	if err := c.BodyParser(&body); err != nil || len(body.Items) == 0 {
		var direct []ReorderMenuItem
		if err2 := c.BodyParser(&direct); err2 != nil || len(direct) == 0 {
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Danh sách items sắp xếp menu là bắt buộc", nil)
		}
		body.Items = direct
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.service.ReorderMenus(ctx, body.Items, getActorUUID(c), c.IP(), c.Get("User-Agent")); err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể sắp xếp lại cây menu", err.Error())
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"reordered": len(body.Items)})
}
