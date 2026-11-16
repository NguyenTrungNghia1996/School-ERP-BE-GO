package audit

import (
	"school-erp/internal/pkg/response"

	"github.com/gofiber/fiber/v2"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// List godoc
// @Summary Truy vấn nhật ký kiểm toán (Audit Logs)
// @Tags Platform Audit
// @Produce json
// @Security BearerAuth
// @Param actor_type query string false "Loại actor"
// @Param actor_id query string false "ID actor"
// @Param organization_id query string false "ID Organization"
// @Param action query string false "Hành động"
// @Param resource_type query string false "Loại tài nguyên"
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} response.SuccessListResponse{data=[]AuditLog}
// @Failure 401 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/platform/audit-logs [get]
func (h *Handler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)

	logs, total, err := h.service.List(ListFilter{
		ActorType:      c.Query("actor_type"),
		ActorID:        c.Query("actor_id"),
		OrganizationID: c.Query("organization_id"),
		Action:         c.Query("action"),
		ResourceType:   c.Query("resource_type"),
		Page:           page,
		Limit:          limit,
	})
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể truy vấn nhật ký kiểm toán", err.Error())
	}

	return response.SuccessWithMeta(c, fiber.StatusOK, logs, response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}
