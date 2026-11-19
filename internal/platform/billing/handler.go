package billing

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

// ListPackages godoc
// @Summary Danh sách gói dịch vụ (PRIMARY / ADDON)
// @Tags Platform Billing
// @Produce json
// @Security BearerAuth
// @Param package_type query string false "Loại gói (PRIMARY, ADDON)"
// @Success 200 {object} response.SuccessResponse{data=[]ServicePackageWithSummary}
// @Failure 401 {object} response.ErrorResponse
// @Router /v1/platform/packages [get]
func (h *Handler) ListPackages(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pkgs, err := h.service.ListPackages(ctx, c.Query("package_type"))
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách gói dịch vụ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, pkgs)
}

// GetPackage godoc
// @Summary Chi tiết gói dịch vụ và các phiên bản
// @Tags Platform Billing
// @Produce json
// @Security BearerAuth
// @Param id path string true "Package ID"
// @Success 200 {object} response.SuccessResponse{data=ServicePackage}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/packages/{id} [get]
func (h *Handler) GetPackage(c *fiber.Ctx) error {
	pkgID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID gói dịch vụ không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pkg, err := h.service.GetPackageDetail(ctx, pkgID)
	if err != nil {
		if errors.Is(err, ErrPackageNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy gói dịch vụ", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy chi tiết gói dịch vụ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, pkg)
}

// CreatePackage godoc
// @Summary Tạo mới gói dịch vụ
// @Tags Platform Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreatePackageInput true "Thông tin gói dịch vụ"
// @Success 201 {object} response.SuccessResponse{data=ServicePackage}
// @Failure 400 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/packages [post]
func (h *Handler) CreatePackage(c *fiber.Ctx) error {
	var body CreatePackageInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}
	pkgType := strings.ToUpper(strings.TrimSpace(body.PackageType))
	if strings.TrimSpace(body.Code) == "" || strings.TrimSpace(body.Name) == "" || (pkgType != "PRIMARY" && pkgType != "ADDON") {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "code, name và package_type (PRIMARY hoặc ADDON) là bắt buộc", nil)
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pkg, err := h.service.CreatePackage(ctx, body)
	if err != nil {
		if errors.Is(err, ErrPackageCodeTaken) {
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Mã gói dịch vụ (code) đã tồn tại", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo gói dịch vụ", err.Error())
	}

	return response.Success(c, fiber.StatusCreated, pkg)
}

// CreatePackageVersion godoc
// @Summary Tạo phiên bản mới (DRAFT) cho gói dịch vụ
// @Tags Platform Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Package ID"
// @Param body body CreateVersionInput true "Thông tin phiên bản"
// @Success 201 {object} response.SuccessResponse{data=ServicePackageVersion}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/packages/{id}/versions [post]
func (h *Handler) CreatePackageVersion(c *fiber.Ctx) error {
	pkgID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID gói dịch vụ không hợp lệ", nil)
	}

	var body CreateVersionInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	body.PackageID = pkgID
	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ver, err := h.service.CreatePackageVersion(ctx, body)
	if err != nil {
		if errors.Is(err, ErrPackageNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy gói dịch vụ", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo phiên bản gói dịch vụ", err.Error())
	}

	return response.Success(c, fiber.StatusCreated, ver)
}

// UpdatePackageVersion godoc
// @Summary Cập nhật phiên bản gói dịch vụ (khi đang ở DRAFT)
// @Tags Platform Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param version_id path string true "Package Version ID"
// @Param body body UpdateVersionInput true "Thông tin cập nhật"
// @Success 200 {object} response.SuccessResponse{data=ServicePackageVersion}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/package-versions/{version_id} [put]
func (h *Handler) UpdatePackageVersion(c *fiber.Ctx) error {
	verID, err := uuid.Parse(c.Params("version_id", c.Params("id")))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID phiên bản gói dịch vụ không hợp lệ", nil)
	}

	var body UpdateVersionInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	body.VersionID = verID
	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ver, err := h.service.UpdatePackageVersion(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrVersionNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy phiên bản gói dịch vụ", nil)
		case errors.Is(err, ErrPackageVersionImmutable):
			return response.Error(c, fiber.StatusConflict, response.ErrCodePackageVersionImmutable, "Phiên bản gói dịch vụ đã PUBLISHED hoặc DEPRECATED là bất biến, không được phép chỉnh sửa", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật phiên bản gói dịch vụ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, ver)
}

// PublishPackageVersion godoc
// @Summary Phát hành (PUBLISH) phiên bản gói dịch vụ
// @Tags Platform Billing
// @Produce json
// @Security BearerAuth
// @Param version_id path string true "Package Version ID"
// @Success 200 {object} response.SuccessResponse{data=ServicePackageVersion}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Failure 422 {object} response.ErrorResponse
// @Router /v1/platform/package-versions/{version_id}/publish [post]
func (h *Handler) PublishPackageVersion(c *fiber.Ctx) error {
	verID, err := uuid.Parse(c.Params("version_id", c.Params("id")))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID phiên bản gói dịch vụ không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ver, err := h.service.PublishPackageVersion(ctx, verID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrVersionNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy phiên bản gói dịch vụ", nil)
		case errors.Is(err, ErrPackageVersionImmutable):
			return response.Error(c, fiber.StatusConflict, response.ErrCodePackageVersionImmutable, "Phiên bản gói dịch vụ này không ở trạng thái DRAFT", nil)
		case errors.Is(err, ErrAddonRequiresFeatures):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodeValidationFailed, "Phiên bản gói dịch vụ phải có ít nhất 1 tính năng (feature_id) trước khi phát hành", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể phát hành phiên bản gói dịch vụ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, ver)
}

// DeprecatePackageVersion godoc
// @Summary Ngừng phát hành (DEPRECATE) phiên bản gói dịch vụ
// @Tags Platform Billing
// @Produce json
// @Security BearerAuth
// @Param version_id path string true "Package Version ID"
// @Success 200 {object} response.SuccessResponse{data=ServicePackageVersion}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/package-versions/{version_id}/deprecate [post]
func (h *Handler) DeprecatePackageVersion(c *fiber.Ctx) error {
	verID, err := uuid.Parse(c.Params("version_id", c.Params("id")))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID phiên bản gói dịch vụ không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ver, err := h.service.DeprecatePackageVersion(ctx, verID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrVersionNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy phiên bản gói dịch vụ", nil)
		case errors.Is(err, ErrPackageVersionImmutable):
			return response.Error(c, fiber.StatusConflict, response.ErrCodePackageVersionImmutable, "Chỉ có thể ngừng phát hành (DEPRECATED) đối với phiên bản đang PUBLISHED", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể chuyển trạng thái DEPRECATED", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, ver)
}

// UpdatePackage godoc
// @Summary Cập nhật thông tin hoặc trạng thái hiển thị của gói dịch vụ
// @Tags Platform Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Package ID"
// @Param body body UpdatePackageInput true "Thông tin cập nhật gói"
// @Success 200 {object} response.SuccessResponse{data=ServicePackage}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/packages/{id} [put]
// @Router /v1/platform/packages/{id} [patch]
func (h *Handler) UpdatePackage(c *fiber.Ctx) error {
	pkgID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID gói dịch vụ không hợp lệ", nil)
	}

	var body UpdatePackageInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pkg, err := h.service.UpdatePackage(ctx, pkgID, body, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrPackageNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy gói dịch vụ", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật gói dịch vụ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, pkg)
}

// DeletePackage godoc
// @Summary Xóa gói dịch vụ (chỉ cho phép khi chưa có phiên bản PUBLISHED và chưa có Tổ chức đăng ký)
// @Tags Platform Billing
// @Produce json
// @Security BearerAuth
// @Param id path string true "Package ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/packages/{id} [delete]
func (h *Handler) DeletePackage(c *fiber.Ctx) error {
	pkgID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID gói dịch vụ không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.service.DeletePackage(ctx, pkgID, getActorUUID(c), c.IP(), c.Get("User-Agent")); err != nil {
		switch {
		case errors.Is(err, ErrPackageNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy gói dịch vụ", nil)
		case errors.Is(err, ErrPackageInUse):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Gói dịch vụ đã phát hành phiên bản hoặc đang có Tổ chức đăng ký, không thể xóa", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa gói dịch vụ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"deleted_package_id": pkgID})
}

// DeletePackageVersion godoc
// @Summary Xóa phiên bản gói dịch vụ đang ở trạng thái DRAFT
// @Tags Platform Billing
// @Produce json
// @Security BearerAuth
// @Param version_id path string true "Package Version ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Router /v1/platform/package-versions/{version_id} [delete]
func (h *Handler) DeletePackageVersion(c *fiber.Ctx) error {
	verID, err := uuid.Parse(c.Params("version_id", c.Params("id")))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID phiên bản gói dịch vụ không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.service.DeletePackageVersion(ctx, verID, getActorUUID(c), c.IP(), c.Get("User-Agent")); err != nil {
		switch {
		case errors.Is(err, ErrVersionNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy phiên bản gói dịch vụ", nil)
		case errors.Is(err, ErrPackageVersionImmutable):
			return response.Error(c, fiber.StatusConflict, response.ErrCodePackageVersionImmutable, "Chỉ có thể xóa phiên bản đang ở trạng thái DRAFT", nil)
		case errors.Is(err, ErrPackageInUse):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Phiên bản đang có Tổ chức đăng ký, không thể xóa", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa phiên bản gói dịch vụ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"deleted_version_id": verID})
}

// ListOrgSubscriptions godoc
// @Summary Danh sách gói dịch vụ đã cấp cho Tổ chức
// @Tags Platform Billing
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Success 200 {object} response.SuccessResponse{data=[]OrganizationSubscription}
// @Failure 400 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/subscriptions [get]
func (h *Handler) ListOrgSubscriptions(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subs, err := h.service.ListOrgSubscriptions(ctx, orgID)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách gói đăng ký của Tổ chức", err.Error())
	}

	return response.Success(c, fiber.StatusOK, subs)
}

// GetOrgEntitlements godoc
// @Summary Tính toán Entitlement hiệu lực của Tổ chức
// @Tags Platform Billing
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Success 200 {object} response.SuccessResponse{data=OrganizationEntitlement}
// @Failure 400 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/entitlements [get]
func (h *Handler) GetOrgEntitlements(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ent, err := h.service.CalculateEntitlement(ctx, orgID)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tính toán Entitlement của Tổ chức", err.Error())
	}

	return response.Success(c, fiber.StatusOK, ent)
}

// CreateSubscription godoc
// @Summary Cấp gói dịch vụ (PRIMARY / ADDON) cho Tổ chức
// @Tags Platform Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param body body CreateSubscriptionInput true "Thông tin cấp gói"
// @Success 201 {object} response.SuccessResponse{data=OrganizationSubscription}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Failure 422 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/subscriptions [post]
func (h *Handler) CreateSubscription(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	var body CreateSubscriptionInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}
	if body.PackageVersionID == uuid.Nil || body.StartedAt.IsZero() || body.ExpiresAt.IsZero() || !body.ExpiresAt.After(body.StartedAt) {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "package_version_id, started_at và expires_at (expires_at > started_at) là bắt buộc", nil)
	}

	body.OrganizationID = orgID
	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub, err := h.service.CreateSubscription(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrVersionNotFound), errors.Is(err, ErrPackageNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy phiên bản gói dịch vụ", nil)
		case errors.Is(err, ErrVersionNotPublished):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodePackageVersionNotPublished, "Chỉ được gán phiên bản gói dịch vụ đang ở trạng thái PUBLISHED", nil)
		case errors.Is(err, ErrPrimaryOverlap):
			return response.Error(c, fiber.StatusConflict, response.ErrCodePrimarySubscriptionOverlap, "Tổ chức đang có gói chính (PRIMARY) bị chồng lấn khoảng thời gian hiệu lực", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cấp gói dịch vụ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, sub)
}

// RenewSubscription godoc
// @Summary Gia hạn gói dịch vụ của Tổ chức
// @Tags Platform Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param sub_id path string true "Subscription ID"
// @Param body body RenewSubscriptionInput true "Thông tin gia hạn"
// @Success 200 {object} response.SuccessResponse{data=OrganizationSubscription}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Failure 422 {object} response.ErrorResponse
// @Router /v1/platform/subscriptions/{sub_id}/renew [post]
func (h *Handler) RenewSubscription(c *fiber.Ctx) error {
	subID, err := uuid.Parse(c.Params("sub_id", c.Params("id")))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID gói đăng ký không hợp lệ", nil)
	}

	var body RenewSubscriptionInput
	if err := c.BodyParser(&body); err != nil || body.NewExpiresAt.IsZero() {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "new_expires_at là bắt buộc", nil)
	}

	body.SubscriptionID = subID
	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub, err := h.service.RenewSubscription(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrSubscriptionNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy gói đăng ký", nil)
		case errors.Is(err, ErrVersionNotPublished):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodePackageVersionNotPublished, "Phiên bản gói dịch vụ gia hạn phải ở trạng thái PUBLISHED", nil)
		case errors.Is(err, ErrPrimaryOverlap):
			return response.Error(c, fiber.StatusConflict, response.ErrCodePrimarySubscriptionOverlap, "Khoảng thời gian gia hạn bị chồng lấn với gói chính khác", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể gia hạn gói dịch vụ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, sub)
}

// SwitchPrimaryPackage godoc
// @Summary Chuyển đổi gói chính (PRIMARY) của Tổ chức
// @Tags Platform Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param body body SwitchPrimaryInput true "Thông tin gói chính mới"
// @Success 200 {object} response.SuccessResponse{data=OrganizationSubscription}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 409 {object} response.ErrorResponse
// @Failure 422 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/switch-primary [post]
func (h *Handler) SwitchPrimaryPackage(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	var body SwitchPrimaryInput
	if err := c.BodyParser(&body); err != nil || body.PackageVersionID == uuid.Nil || body.NewExpiresAt.IsZero() {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "package_version_id và new_expires_at là bắt buộc", nil)
	}

	body.OrganizationID = orgID
	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub, err := h.service.SwitchPrimaryPackage(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrVersionNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy phiên bản gói dịch vụ", nil)
		case errors.Is(err, ErrVersionNotPublished):
			return response.Error(c, fiber.StatusUnprocessableEntity, response.ErrCodePackageVersionNotPublished, "Phiên bản gói mới phải là gói PRIMARY và ở trạng thái PUBLISHED", nil)
		case errors.Is(err, ErrPrimaryOverlap):
			return response.Error(c, fiber.StatusConflict, response.ErrCodePrimarySubscriptionOverlap, "Trùng lặp thời gian gói chính", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể chuyển đổi gói chính", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, sub)
}

// SuspendSubscription godoc
// @Summary Tạm ngưng gói dịch vụ của Tổ chức
// @Tags Platform Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param sub_id path string true "Subscription ID"
// @Param body body map[string]string true "reason"
// @Success 200 {object} response.SuccessResponse{data=OrganizationSubscription}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/subscriptions/{sub_id}/suspend [patch]
func (h *Handler) SuspendSubscription(c *fiber.Ctx) error {
	subID, err := uuid.Parse(c.Params("sub_id", c.Params("id")))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID gói đăng ký không hợp lệ", nil)
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Reason) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Bắt buộc nhập lý do (reason) khi tạm ngưng gói dịch vụ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub, err := h.service.SuspendSubscription(ctx, subID, strings.TrimSpace(body.Reason), getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy gói đăng ký", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạm ngưng gói dịch vụ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, sub)
}

// RevokeSubscription godoc
// @Summary Thu hồi gói dịch vụ của Tổ chức
// @Tags Platform Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param sub_id path string true "Subscription ID"
// @Param body body map[string]string true "reason"
// @Success 200 {object} response.SuccessResponse{data=OrganizationSubscription}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/subscriptions/{sub_id}/revoke [patch]
func (h *Handler) RevokeSubscription(c *fiber.Ctx) error {
	subID, err := uuid.Parse(c.Params("sub_id", c.Params("id")))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID gói đăng ký không hợp lệ", nil)
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Reason) == "" {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Bắt buộc nhập lý do (reason) khi thu hồi gói dịch vụ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub, err := h.service.RevokeSubscription(ctx, subID, strings.TrimSpace(body.Reason), getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy gói đăng ký", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể thu hồi gói dịch vụ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, sub)
}
