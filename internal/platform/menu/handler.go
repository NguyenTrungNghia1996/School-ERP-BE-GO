package menu

import (
	"context"
	"school-erp/internal/pkg/response"
	"time"

	"github.com/gofiber/fiber/v2"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// GetTree godoc
// @Summary Lấy cây menu nền tảng (Platform Menu Tree)
// @Tags Platform IAM
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.SuccessResponse{data=[]AntDMenuNode}
// @Failure 401 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/platform/menus/tree [get]
func (h *Handler) GetTree(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tree, err := h.service.GetFullMenuTree(ctx)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải cây menu nền tảng", err.Error())
	}

	return response.Success(c, fiber.StatusOK, tree)
}
