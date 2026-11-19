package org_school

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

func getActorUUID(c *fiber.Ctx) uuid.UUID {
	if s, ok := c.Locals("tenant_user_id").(string); ok {
		if id, err := uuid.Parse(s); err == nil {
			return id
		}
	}
	return uuid.Nil
}

// GetOrganization godoc
// @Summary Xem thông tin Đơn vị và trạng thái gói dịch vụ đang sử dụng
// @Tags Tenant Organization
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Success 200 {object} response.SuccessResponse{data=OrganizationOverviewOutput}
// @Failure 401 {object} response.ErrorResponse
// @Router /v1/organization [get]
func (h *Handler) GetOrganization(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := h.service.GetOrganizationOverview(ctx, tc)
	if err != nil {
		if errors.Is(err, ErrOrgNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Đơn vị", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải thông tin Đơn vị", err.Error())
	}
	return response.Success(c, fiber.StatusOK, out)
}

// UpdateOrganization godoc
// @Summary Cập nhật thông tin liên hệ, địa chỉ, logo của Đơn vị (không đổi subdomain)
// @Tags Tenant Organization
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param body body UpdateOrganizationInput true "Thông tin cập nhật"
// @Success 200 {object} response.SuccessResponse{data=Organization}
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/organization [patch]
func (h *Handler) UpdateOrganization(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	var body UpdateOrganizationInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	org, err := h.service.UpdateOrganization(ctx, tc, body, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật thông tin Đơn vị", err.Error())
	}
	return response.Success(c, fiber.StatusOK, org)
}

// ListSchools godoc
// @Summary Danh sách các trường học trực thuộc Đơn vị (cô lập bởi RLS)
// @Tags Tenant Schools
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Success 200 {object} response.SuccessResponse{data=[]School}
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/schools [get]
func (h *Handler) ListSchools(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	schools, err := h.service.ListSchools(ctx, tc)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách trường học", err.Error())
	}
	return response.Success(c, fiber.StatusOK, schools)
}

// CreateSchool godoc
// @Summary Tạo trường học mới trực thuộc Đơn vị (kiểm tra hạn mức max_schools)
// @Tags Tenant Schools
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param body body CreateSchoolInput true "Thông tin trường mới"
// @Success 201 {object} response.SuccessResponse{data=School}
// @Failure 400 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Failure 422 {object} response.ErrorResponse
// @Router /v1/schools [post]
func (h *Handler) CreateSchool(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	var body CreateSchoolInput
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Code) == "" || strings.TrimSpace(body.Name) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "code và name là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	school, err := h.service.CreateSchool(ctx, tc, body, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrSchoolQuotaExceeded):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodeQuotaExceeded, "Số lượng trường đã đạt hạn mức max_schools của gói dịch vụ", nil)
		case errors.Is(err, ErrSchoolCodeTaken):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Mã trường (code) đã tồn tại trong Đơn vị", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo trường học mới", err.Error())
		}
	}
	return response.Success(c, fiber.StatusCreated, school)
}

// UpdateSchool godoc
// @Summary Cập nhật thông tin trường học trực thuộc Đơn vị
// @Tags Tenant Schools
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param id path string true "School ID"
// @Param body body UpdateSchoolInput true "Thông tin cập nhật"
// @Success 200 {object} response.SuccessResponse{data=School}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/schools/{id} [put]
func (h *Handler) UpdateSchool(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	schoolID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID trường học không hợp lệ", nil)
	}

	var body UpdateSchoolInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	school, err := h.service.UpdateSchool(ctx, tc, schoolID, body, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidSchoolStatus):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Trạng thái trường không hợp lệ", nil)
		case errors.Is(err, ErrSchoolNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy trường học", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật trường học", err.Error())
		}
	}
	return response.Success(c, fiber.StatusOK, school)
}
