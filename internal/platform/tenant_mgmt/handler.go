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

// ListOrganizations godoc
// @Summary Danh sách Tổ chức (Organizations)
// @Tags Platform Organizations
// @Produce json
// @Security BearerAuth
// @Param search query string false "Tìm kiếm"
// @Param status query string false "Trạng thái"
// @Param dns_status query string false "Trạng thái DNS"
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} response.SuccessListResponse{data=[]OrganizationSummary}
// @Failure 401 {object} response.ErrorResponse
// @Router /v1/platform/organizations [get]
func (h *Handler) ListOrganizations(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	search := c.Query("search", c.Query("q"))

	orgs, total, err := h.service.ListOrganizations(ctx, ListOrgsFilter{
		Search:    search,
		Status:    c.Query("status"),
		DNSStatus: c.Query("dns_status"),
		Page:      page,
		Limit:     limit,
	})
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách Tổ chức", err.Error())
	}

	return response.SuccessWithMeta(c, fiber.StatusOK, orgs, response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// GetOrganization godoc
// @Summary Chi tiết Tổ chức
// @Tags Platform Organizations
// @Produce json
// @Security BearerAuth
// @Param id path string true "Organization ID"
// @Success 200 {object} response.SuccessResponse{data=Organization}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{id} [get]
func (h *Handler) GetOrganization(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	detail, err := h.service.GetOrganizationDetail(ctx, orgID)
	if err != nil {
		if errors.Is(err, ErrOrgNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Tổ chức", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy chi tiết Tổ chức", err.Error())
	}

	return response.Success(c, fiber.StatusOK, detail)
}

// CreateOrganization godoc
// @Summary Tạo mới Tổ chức (kèm trường đầu tiên và School Admin)
// @Tags Platform Organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateOrganizationInput true "Thông tin khởi tạo Tổ chức"
// @Success 201 {object} response.SuccessResponse{data=Organization}
// @Failure 400 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/organizations [post]
func (h *Handler) CreateOrganization(c *fiber.Ctx) error {
	var body CreateOrganizationInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu đầu vào không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Subdomain) == "" ||
		strings.TrimSpace(body.Admin.Email) == "" || strings.TrimSpace(body.Admin.Password) == "" || strings.TrimSpace(body.Admin.FullName) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Tên tổ chức, subdomain và thông tin tài khoản School Admin đầu tiên là bắt buộc", nil)
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := h.service.CreateOrganization(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrSubdomainInvalidFormat):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Subdomain không đúng định dạng (chỉ a-z, 0-9, -, dài 3-40 ký tự, không bắt đầu/kết thúc bằng -)", nil)
		case errors.Is(err, ErrSubdomainReservedOrTaken):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeSubdomainReservedOrTaken, "Subdomain nằm trong danh sách giữ chỗ hoặc đã có Tổ chức sử dụng", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo Tổ chức", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, result)
}

// ApproveOrganization godoc
// @Summary Phê duyệt Tổ chức
// @Tags Platform Organizations
// @Produce json
// @Security BearerAuth
// @Param id path string true "Organization ID"
// @Success 200 {object} response.SuccessResponse{data=Organization}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{id}/approve [post]
func (h *Handler) ApproveOrganization(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	org, err := h.service.ApproveOrganization(ctx, orgID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrOrgNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Tổ chức", nil)
		case errors.Is(err, ErrOrgNotPending):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Tổ chức không ở trạng thái chờ phê duyệt (PENDING_APPROVAL)", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể phê duyệt Tổ chức", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, org)
}

// UpdateOrganizationStatus godoc
// @Summary Cập nhật trạng thái Tổ chức (ACTIVE / SUSPENDED)
// @Tags Platform Organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Organization ID"
// @Param body body map[string]string true "status, reason"
// @Success 200 {object} response.SuccessResponse{data=Organization}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{id}/status [patch]
func (h *Handler) UpdateOrganizationStatus(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	status := strings.ToUpper(strings.TrimSpace(body.Status))
	if status != "ACTIVE" && status != "SUSPENDED" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Trạng thái phải là ACTIVE hoặc SUSPENDED", nil)
	}
	if strings.TrimSpace(body.Reason) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Bắt buộc nhập lý do (reason) khi thay đổi trạng thái Tổ chức", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	org, err := h.service.UpdateOrganizationStatus(ctx, orgID, status, strings.TrimSpace(body.Reason), getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrOrgNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Tổ chức", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật trạng thái Tổ chức", err.Error())
	}

	return response.Success(c, fiber.StatusOK, org)
}

// ChangeSubdomain godoc
// @Summary Đổi Subdomain chính của Tổ chức
// @Tags Platform Organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Organization ID"
// @Param body body map[string]any true "new_subdomain, reason, redirect_until"
// @Success 200 {object} response.SuccessResponse{data=Organization}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{id}/change-subdomain [post]
func (h *Handler) ChangeSubdomain(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	var body struct {
		NewSubdomain  string     `json:"new_subdomain"`
		Reason        string     `json:"reason"`
		RedirectUntil *time.Time `json:"redirect_until"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.NewSubdomain) == "" || strings.TrimSpace(body.Reason) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Subdomain mới và lý do (reason) là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	org, err := h.service.ChangeSubdomain(ctx, orgID, body.NewSubdomain, strings.TrimSpace(body.Reason), body.RedirectUntil, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrOrgNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Tổ chức", nil)
		case errors.Is(err, ErrSubdomainInvalidFormat):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Subdomain mới không đúng định dạng", nil)
		case errors.Is(err, ErrSubdomainReservedOrTaken):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeSubdomainReservedOrTaken, "Subdomain nằm trong danh sách giữ chỗ hoặc đã có Tổ chức sử dụng", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể đổi subdomain", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, org)
}

// ListReservedSubdomains godoc
// @Summary Danh sách từ khóa Subdomain bị giữ chỗ (Reserved)
// @Tags Platform Organizations
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.SuccessResponse{data=[]ReservedSubdomain}
// @Failure 401 {object} response.ErrorResponse
// @Router /v1/platform/reserved-subdomains [get]
func (h *Handler) ListReservedSubdomains(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	items, err := h.service.ListReservedSubdomains(ctx)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách subdomain giữ chỗ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, items)
}

// AddReservedSubdomain godoc
// @Summary Thêm từ khóa Subdomain giữ chỗ
// @Tags Platform Organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]string true "keyword, reason"
// @Success 201 {object} response.SuccessResponse{data=ReservedSubdomain}
// @Failure 400 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/reserved-subdomains [post]
func (h *Handler) AddReservedSubdomain(c *fiber.Ctx) error {
	var body struct {
		Keyword string `json:"keyword"`
		Reason  string `json:"reason"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.Keyword) == "" || strings.TrimSpace(body.Reason) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Từ khóa (keyword) và lý do (reason) là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	item, err := h.service.AddReservedSubdomain(ctx, body.Keyword, body.Reason, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		return response.Error(c, fiber.StatusConflict, response.ErrCodeSubdomainReservedOrTaken, "Từ khóa subdomain này đã tồn tại", err.Error())
	}

	return response.Success(c, fiber.StatusCreated, item)
}
