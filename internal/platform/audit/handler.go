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
