package iam

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

// ListMembers godoc
// @Summary Danh sách thành viên thuộc trường hiện tại
// @Tags Tenant IAM
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param X-School-ID header string false "School ID đang chọn"
// @Param search query string false "Tìm kiếm"
// @Param status query string false "Trạng thái (ACTIVE, SUSPENDED, INACTIVE)"
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} response.SuccessListResponse{data=[]SchoolMembership}
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/iam/members [get]
func (h *Handler) ListMembers(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	members, total, err := h.service.ListMembers(ctx, tc, c.Query("search", c.Query("q")), c.Query("status"), page, limit)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách thành viên", err.Error())
	}
	return response.SuccessWithMeta(c, fiber.StatusOK, members, response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// CreateMember godoc
// @Summary Thêm thành viên vào trường (tái sử dụng user nếu email/phone đã tồn tại - Single Account Identity)
// @Tags Tenant IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param X-School-ID header string false "School ID đang chọn"
// @Param body body CreateMemberInput true "Thông tin thành viên"
// @Success 201 {object} response.SuccessResponse{data=SchoolMembership}
// @Failure 400 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/iam/members [post]
func (h *Handler) CreateMember(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	var body CreateMemberInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}
	if strings.TrimSpace(body.FullName) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "full_name là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m, err := h.service.CreateMember(ctx, tc, body, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidMemberType), errors.Is(err, ErrWeakPassword):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, ErrMemberAlreadyExists):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Tài khoản này đã là thành viên của trường", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể thêm thành viên vào trường", err.Error())
		}
	}
	return response.Success(c, fiber.StatusCreated, m)
}

// UpdateMemberRoles godoc
// @Summary Cập nhật danh sách vai trò của thành viên (chống gỡ SCHOOL_ADMIN cuối cùng)
// @Tags Tenant IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param membership_id path string true "Membership ID"
// @Param body body UpdateMemberRolesInput true "Danh sách role_ids"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 422 {object} response.ErrorResponse
// @Router /v1/iam/members/{membership_id}/roles [put]
func (h *Handler) UpdateMemberRoles(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	membershipID, err := uuid.Parse(c.Params("membership_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "membership_id không hợp lệ", nil)
	}

	var body UpdateMemberRolesInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu role_ids không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h.service.UpdateMemberRoles(ctx, tc, membershipID, body.RoleIDs, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrMemberNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy thành viên", nil)
		case errors.Is(err, ErrLastSchoolAdminProtected):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodeLastSchoolAdminProtected, "Không thể gỡ vai trò SCHOOL_ADMIN khỏi quản trị viên cuối cùng của trường", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật vai trò thành viên", err.Error())
		}
	}
	return response.Success(c, fiber.StatusOK, fiber.Map{"membership_id": membershipID, "role_ids": body.RoleIDs})
}

// ListEntitledPermissions godoc
// @Summary Danh sách quyền từ Catalog mà Đơn vị được phép dùng theo gói dịch vụ
// @Tags Tenant IAM
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Success 200 {object} response.SuccessResponse{data=[]EntitledPermissionItem}
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/iam/entitled-permissions [get]
func (h *Handler) ListEntitledPermissions(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	orgID := uuid.MustParse(tc.OrganizationID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	items, err := h.service.ListEntitledPermissions(ctx, orgID)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách quyền khả dụng", err.Error())
	}
	return response.Success(c, fiber.StatusOK, items)
}

// ListRoles godoc
// @Summary Danh sách vai trò của Đơn vị
// @Tags Tenant IAM
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Success 200 {object} response.SuccessResponse{data=[]TenantRole}
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/iam/roles [get]
func (h *Handler) ListRoles(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	roles, err := h.service.ListRoles(ctx, tc)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách vai trò", err.Error())
	}
	return response.Success(c, fiber.StatusOK, roles)
}

// CreateRole godoc
// @Summary Tạo vai trò tùy chỉnh của Đơn vị (kiểm tra Entitlement cho từng permission_id)
// @Tags Tenant IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param body body CreateRoleInput true "Thông tin vai trò"
// @Success 201 {object} response.SuccessResponse{data=TenantRole}
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/iam/roles [post]
func (h *Handler) CreateRole(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	var body CreateRoleInput
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Code) == "" || strings.TrimSpace(body.Name) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "code và name là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	role, err := h.service.CreateRole(ctx, tc, body, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidRoleCode), errors.Is(err, ErrInvalidDataScope):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, ErrPermissionNotEntitled):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeFeatureNotSubscribed, "Một số quyền được chọn không thuộc gói tính năng của Đơn vị", nil)
		case errors.Is(err, ErrRoleCodeTaken):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Mã vai trò đã tồn tại", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo vai trò", err.Error())
		}
	}
	return response.Success(c, fiber.StatusCreated, role)
}

// UpdateRole godoc
// @Summary Cập nhật vai trò và quyền (tự động xóa cache Redis của toàn bộ thành viên giữ vai trò này)
// @Tags Tenant IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param id path string true "Role ID"
// @Param body body UpdateRoleInput true "Thông tin cập nhật vai trò"
// @Success 200 {object} response.SuccessResponse{data=TenantRole}
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/iam/roles/{id} [put]
func (h *Handler) UpdateRole(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	roleID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID vai trò không hợp lệ", nil)
	}

	var body UpdateRoleInput
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "name là bắt buộc", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	role, err := h.service.UpdateRole(ctx, tc, roleID, body, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidDataScope):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, ErrPermissionNotEntitled):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeFeatureNotSubscribed, "Một số quyền được chọn không thuộc gói tính năng của Đơn vị", nil)
		case errors.Is(err, ErrRoleNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy vai trò", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật vai trò", err.Error())
		}
	}
	return response.Success(c, fiber.StatusOK, role)
}

// DeleteRole godoc
// @Summary Xóa vai trò tùy chỉnh của Đơn vị
// @Tags Tenant IAM
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param id path string true "Role ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/iam/roles/{id} [delete]
func (h *Handler) DeleteRole(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	roleID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID vai trò không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h.service.DeleteRole(ctx, tc, roleID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrRoleNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy vai trò", nil)
		case errors.Is(err, ErrSystemRoleProtected):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeSystemRoleProtected, "Không được phép xóa vai trò hệ thống (is_system = true)", nil)
		case errors.Is(err, ErrRoleInUse):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeRoleInUse, "Không thể xóa vai trò vì đang có thành viên nắm giữ", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa vai trò", err.Error())
		}
	}
	return response.Success(c, fiber.StatusOK, fiber.Map{"deleted_role_id": roleID})
}

// UpdateMemberStatus godoc
// @Summary Khóa / Mở khóa hoặc tạm ngưng thành viên trong trường
// @Tags Tenant IAM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param membership_id path string true "Membership ID"
// @Param body body UpdateMemberStatusInput true "status, reason"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 422 {object} response.ErrorResponse
// @Router /v1/iam/members/{membership_id}/status [patch]
func (h *Handler) UpdateMemberStatus(c *fiber.Ctx) error {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	membershipID, err := uuid.Parse(c.Params("membership_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "membership_id không hợp lệ", nil)
	}

	var body UpdateMemberStatusInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h.service.UpdateMemberStatus(ctx, tc, membershipID, body.Status, strings.TrimSpace(body.Reason), getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidMemberStatus):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, ErrSelfModificationNotAllow):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeSelfModificationNotAllowed, "Không thể tự khóa tài khoản của chính mình", nil)
		case errors.Is(err, ErrMemberNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy thành viên", nil)
		case errors.Is(err, ErrLastSchoolAdminProtected):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodeLastSchoolAdminProtected, "Không thể khóa quản trị viên SCHOOL_ADMIN cuối cùng của trường", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật trạng thái thành viên", err.Error())
		}
	}
	return response.Success(c, fiber.StatusOK, fiber.Map{"membership_id": membershipID, "status": strings.ToUpper(strings.TrimSpace(body.Status))})
}
