package tenant_mgmt

import (
	"context"
	"errors"
	"school-erp/internal/pkg/response"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// CheckSubdomain godoc
// @Summary Kiểm tra tính hợp lệ và khả dụng của Subdomain
// @Tags Platform Organizations
// @Produce json
// @Security BearerAuth
// @Param subdomain query string true "Subdomain cần kiểm tra"
// @Success 200 {object} map[string]any
// @Router /v1/platform/organizations/check-subdomain [get]
func (h *Handler) CheckSubdomain(c *fiber.Ctx) error {
	subdomain := strings.TrimSpace(c.Query("subdomain"))
	if subdomain == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Tham số query 'subdomain' là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := h.service.CheckSubdomainAvailability(ctx, subdomain)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể kiểm tra subdomain", err.Error())
	}

	return response.Success(c, fiber.StatusOK, res)
}

// RetryDNS godoc
// @Summary Thử lại đồng bộ bản ghi Cloudflare DNS cho Tổ chức
// @Tags Platform Organizations
// @Produce json
// @Security BearerAuth
// @Param id path string true "Organization ID"
// @Success 200 {object} map[string]any
// @Router /v1/platform/organizations/{id}/dns/retry [post]
func (h *Handler) RetryDNS(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	org, err := h.service.RetryOrganizationDNS(ctx, orgID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrOrgNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Tổ chức", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể kích hoạt lại đồng bộ DNS", err.Error())
	}

	return response.Success(c, fiber.StatusOK, org)
}

// DeleteRedirectHistoryDNS godoc
// @Summary Xóa bản ghi Cloudflare DNS của Subdomain cũ trong lịch sử Redirect
// @Tags Platform Organizations
// @Produce json
// @Security BearerAuth
// @Param history_id path string true "Subdomain History ID"
// @Success 200 {object} map[string]any
// @Router /v1/platform/subdomain-histories/{history_id}/dns [delete]
func (h *Handler) DeleteRedirectHistoryDNS(c *fiber.Ctx) error {
	historyID, err := uuid.Parse(c.Params("history_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID lịch sử subdomain không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	history, err := h.service.DeleteRedirectHistoryDNS(ctx, historyID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrHistoryNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy bản ghi lịch sử subdomain", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa bản ghi DNS của subdomain cũ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, history)
}

// PublicTenantResolve godoc
// @Summary Phân giải Subdomain của Tổ chức & Redirect 301/308 (Public)
// @Tags Public Tenant
// @Produce json
// @Param subdomain query string false "Subdomain cần phân giải"
// @Param X-Tenant-Subdomain header string false "Subdomain qua Header"
// @Success 200 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Router /v1/public/tenant-resolve [get]
func (h *Handler) PublicTenantResolve(c *fiber.Ctx) error {
	subdomain := strings.TrimSpace(c.Query("subdomain"))
	if subdomain == "" {
		subdomain = strings.TrimSpace(c.Get("X-Tenant-Subdomain"))
	}
	if subdomain == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Tham số 'subdomain' là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := h.service.ResolveTenantBySubdomain(ctx, subdomain)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Lỗi phân giải thông tin Tổ chức", err.Error())
	}

	if action, _ := res["action"].(string); action == "NOT_FOUND" {
		return response.Success(c, fiber.StatusNotFound, res)
	}

	return response.Success(c, fiber.StatusOK, res)
}
