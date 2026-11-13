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

func (h *Handler) ListPackages(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pkgs, err := h.service.ListPackages(ctx, c.Query("package_type"))
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách gói dịch vụ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, pkgs)
}

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

func (h *Handler) UpdatePackageVersion(c *fiber.Ctx) error {
	verID, err := uuid.Parse(c.Params("id"))
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
			return response.Error(c, fiber.StatusConflict, response.ErrCodePackageVersionImmutable, "Phiên bản gói dịch vụ đã PUBLISHED là bất biến, không được phép chỉnh sửa", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cập nhật phiên bản gói dịch vụ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, ver)
}

func (h *Handler) PublishPackageVersion(c *fiber.Ctx) error {
	verID, err := uuid.Parse(c.Params("id"))
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
			return response.Error(c, fiber.StatusConflict, response.ErrCodePackageVersionImmutable, "Phiên bản gói dịch vụ này đã được PUBLISHED trước đó", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể phát hành phiên bản gói dịch vụ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, ver)
}

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
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Chỉ được cấp phiên bản gói dịch vụ đã PUBLISHED", nil)
		case errors.Is(err, ErrPrimaryOverlap):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Tổ chức đang có gói chính (PRIMARY) còn hiệu lực bị chồng lấn thời gian", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể cấp gói dịch vụ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, sub)
}

func (h *Handler) RevokeSubscription(c *fiber.Ctx) error {
	subID, err := uuid.Parse(c.Params("id"))
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
