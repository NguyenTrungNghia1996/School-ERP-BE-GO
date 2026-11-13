package support

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

func (h *Handler) ListSessions(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)

	sessions, total, err := h.service.ListSessions(ctx, ListFilter{
		Status:         c.Query("status"),
		OrganizationID: c.Query("organization_id"),
		Page:           page,
		Limit:          limit,
	})
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy danh sách phiên hỗ trợ", err.Error())
	}

	return response.SuccessWithMeta(c, fiber.StatusOK, sessions, response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

func (h *Handler) CreateSession(c *fiber.Ctx) error {
	var body CreateSessionInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}
	sessionType := strings.ToUpper(strings.TrimSpace(body.SessionType))
	if body.OrganizationID == uuid.Nil || strings.TrimSpace(body.Reason) == "" || (sessionType != "READ_ONLY" && sessionType != "READ_WRITE") {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "organization_id, reason và session_type (READ_ONLY hoặc READ_WRITE) là bắt buộc", nil)
	}

	body.ActorID = getActorUUID(c)
	body.IPAddress = c.IP()
	body.UserAgent = c.Get("User-Agent")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := h.service.CreateSession(ctx, body)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tạo yêu cầu phiên hỗ trợ", err.Error())
	}

	return response.Success(c, fiber.StatusCreated, session)
}

func (h *Handler) ApproveSession(c *fiber.Ctx) error {
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID phiên hỗ trợ không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := h.service.ApproveSession(ctx, sessionID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy phiên hỗ trợ", nil)
		case errors.Is(err, ErrSelfApprovalNotAllowed):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodeSelfApprovalNotAllowed, "Vi phạm nguyên tắc Four-Eyes: không được tự phê duyệt phiên hỗ trợ do chính bạn yêu cầu", nil)
		case errors.Is(err, ErrSessionNotPending):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Phiên hỗ trợ không ở trạng thái chờ phê duyệt (PENDING)", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể phê duyệt phiên hỗ trợ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, session)
}

func (h *Handler) ExchangeToken(c *fiber.Ctx) error {
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID phiên hỗ trợ không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := h.service.ExchangeToken(ctx, sessionID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy phiên hỗ trợ", nil)
		case errors.Is(err, ErrNotSessionOwner):
			return response.Error(c, fiber.StatusForbidden, response.ErrCodePlatformPermissionDenied, "Chỉ người tạo yêu cầu phiên hỗ trợ mới được phép đổi token", nil)
		case errors.Is(err, ErrSessionNotApproved):
			return response.Error(c, fiber.StatusConflict, response.ErrCodeConflict, "Phiên hỗ trợ chưa được phê duyệt (APPROVED)", nil)
		case errors.Is(err, ErrSessionExpired):
			return response.Error(c, fiber.StatusGone, response.ErrCodeSessionExpired, "Phiên hỗ trợ đã hết hạn", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể đổi token phiên hỗ trợ", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, res)
}

func (h *Handler) RevokeSession(c *fiber.Ctx) error {
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID phiên hỗ trợ không hợp lệ", nil)
	}

	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.BodyParser(&body)
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = "Thu hồi bởi quản trị viên nền tảng"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := h.service.RevokeSession(ctx, sessionID, reason, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy phiên hỗ trợ", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể thu hồi phiên hỗ trợ", err.Error())
	}

	return response.Success(c, fiber.StatusOK, session)
}
