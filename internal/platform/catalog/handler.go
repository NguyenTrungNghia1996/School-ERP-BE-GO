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

func (h *Handler) ListFeatures(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	features, err := h.service.ListFeatures(ctx)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách tính năng", err.Error())
	}

	return response.Success(c, fiber.StatusOK, features)
}

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
		if errors.Is(err, ErrCodeAlreadyExists) {
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Mã tính năng (code) đã tồn tại", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo tính năng", err.Error())
	}

	return response.Success(c, fiber.StatusCreated, feature)
}

func (h *Handler) ListPermissions(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	perms, err := h.service.ListPermissions(ctx, c.Query("feature_id"))
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách quyền Tenant", err.Error())
	}

	return response.Success(c, fiber.StatusOK, perms)
}

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
		case errors.Is(err, ErrCodeAlreadyExists):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Mã quyền (code) đã tồn tại", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo quyền Tenant", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, perm)
}

func (h *Handler) GetMenuTree(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tree, err := h.service.GetTenantBaseMenuTree(ctx)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy cây menu nền Tenant", err.Error())
	}

	return response.Success(c, fiber.StatusOK, tree)
}

func (h *Handler) UpdateMenuTree(c *fiber.Ctx) error {
	var body struct {
		Menus []TenantMenuTreeNode `json:"menus"`
	}
	if err := c.BodyParser(&body); err != nil {
		// Hỗ trợ truyền mảng trực tiếp hoặc bọc trong {"menus": [...]}
		var direct []TenantMenuTreeNode
		if err2 := c.BodyParser(&direct); err2 != nil {
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Cấu trúc cây menu không hợp lệ", err.Error())
		}
		body.Menus = direct
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	updated, err := h.service.ReplaceTenantBaseMenuTree(ctx, body.Menus, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật cây menu nền Tenant", err.Error())
	}

	return response.Success(c, fiber.StatusOK, updated)
}
