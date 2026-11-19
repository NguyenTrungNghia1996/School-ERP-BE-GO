package media

import (
	"context"
	"errors"
	"io"
	"school-erp/internal/pkg/database"
	"school-erp/internal/pkg/response"
	platformmedia "school-erp/internal/platform/media"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type Handler struct {
	service *platformmedia.Service
}

func NewHandler(service *platformmedia.Service) *Handler {
	return &Handler{service: service}
}

func parseTenantOrgID(c *fiber.Ctx) (uuid.UUID, database.TenantContext, error) {
	tc, _ := c.Locals("tenant_ctx").(database.TenantContext)
	orgID, err := uuid.Parse(tc.OrganizationID)
	return orgID, tc, err
}

func getTenantActorUUID(c *fiber.Ctx) uuid.UUID {
	if s, ok := c.Locals("tenant_user_id").(string); ok {
		if id, err := uuid.Parse(s); err == nil {
			return id
		}
	}
	return uuid.Nil
}

// UploadImage godoc
// @Summary Upload ảnh hoặc tài liệu (PDF, Word, Excel) của Đơn vị / Trường học lên S3/R2
// @Tags Tenant Media
// @Accept mpfd
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param X-School-ID header string false "School ID ngữ cảnh hiện tại"
// @Param file formData file true "Tệp ảnh (JPEG, PNG, WEBP, GIF) hoặc tài liệu (PDF, DOC, DOCX, XLS, XLSX; tối đa 25 MB)"
// @Param folder formData string false "Thư mục con (VD: logos, avatars, students, documents; mặc định: general)"
// @Param school_id formData string false "School ID (truyền UUID trường hoặc 'current' để lưu theo trường hiện tại; bỏ trống để lưu cấp đơn vị)"
// @Success 201 {object} response.SuccessResponse{data=media.StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 413 {object} response.ErrorResponse
// @Failure 503 {object} response.ErrorResponse
// @Router /v1/media/upload [post]
func (h *Handler) UploadImage(c *fiber.Ctx) error {
	orgID, tc, err := parseTenantOrgID(c)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Thiếu tệp ảnh (field 'file')", nil)
	}

	var schoolIDPtr *uuid.UUID
	rawSchoolID := strings.TrimSpace(c.FormValue("school_id"))
	if strings.EqualFold(rawSchoolID, "current") {
		rawSchoolID = strings.TrimSpace(tc.SchoolID)
	}
	if rawSchoolID != "" {
		parsed, err := uuid.Parse(rawSchoolID)
		if err != nil {
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "school_id không hợp lệ", nil)
		}
		schoolIDPtr = &parsed
	}

	f, err := fh.Open()
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Không thể đọc tệp tải lên", err.Error())
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, platformmedia.MaxImageSizeBytes()+1))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Lỗi đọc dữ liệu tệp ảnh", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	record, err := h.service.UploadImage(ctx, platformmedia.UploadImageInput{
		OrganizationID: orgID,
		SchoolID:       schoolIDPtr,
		UploadedBy:     getTenantActorUUID(c),
		ActorType:      "TENANT",
		Folder:         c.FormValue("folder"),
		OriginalName:   fh.Filename,
		Data:           data,
		IPAddress:      c.IP(),
		UserAgent:      c.Get("User-Agent"),
	})
	if err != nil {
		switch {
		case errors.Is(err, platformmedia.ErrStorageNotConfigured):
			return response.Error(c, fiber.StatusServiceUnavailable, response.ErrCodeStorageNotConfigured, "Dịch vụ lưu trữ S3/R2 chưa được cấu hình", nil)
		case errors.Is(err, platformmedia.ErrOrgNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Đơn vị", nil)
		case errors.Is(err, platformmedia.ErrSchoolNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Trường học thuộc Đơn vị này", nil)
		case errors.Is(err, platformmedia.ErrInvalidImageType), errors.Is(err, platformmedia.ErrInvalidFolder):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, platformmedia.ErrFileTooLarge):
			return response.Error(c, fiber.StatusRequestEntityTooLarge, response.ErrCodeValidationFailed, "Kích thước ảnh vượt quá giới hạn cho phép", nil)
		case errors.Is(err, platformmedia.ErrStorageQuotaExceeded):
			return response.Error(c, fiber.StatusRequestEntityTooLarge, response.ErrCodeStorageQuotaExceeded, "Dung lượng lưu trữ của Đơn vị đã đạt hạn mức gói dịch vụ", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải ảnh lên S3/R2", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, record)
}

// GetUsage godoc
// @Summary Thống kê dung lượng lưu trữ ảnh của Đơn vị hiện tại
// @Tags Tenant Media
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Success 200 {object} response.SuccessResponse{data=media.StorageUsageOutput}
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/storage/usage [get]
func (h *Handler) GetUsage(c *fiber.Ctx) error {
	orgID, _, err := parseTenantOrgID(c)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	usage, err := h.service.GetUsage(ctx, orgID)
	if err != nil {
		if errors.Is(err, platformmedia.ErrOrgNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Đơn vị", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy thống kê dung lượng", err.Error())
	}

	return response.Success(c, fiber.StatusOK, usage)
}

// ListFiles godoc
// @Summary Danh sách ảnh đã tải lên của Đơn vị hiện tại
// @Tags Tenant Media
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param folder query string false "Lọc theo thư mục con"
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} response.SuccessListResponse{data=[]media.StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/media [get]
func (h *Handler) ListFiles(c *fiber.Ctx) error {
	orgID, _, err := parseTenantOrgID(c)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	files, total, err := h.service.ListFiles(ctx, orgID, c.Query("folder"), page, limit)
	if err != nil {
		if errors.Is(err, platformmedia.ErrInvalidFolder) {
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Tên thư mục không hợp lệ", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách ảnh", err.Error())
	}

	return response.SuccessWithMeta(c, fiber.StatusOK, files, response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// GetFileURL godoc
// @Summary Lấy đường dẫn truy cập (Public hoặc Presigned URL 15 phút) của tệp ảnh thuộc Đơn vị
// @Tags Tenant Media
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse{data=media.MediaFileURLOutput}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/media/{file_id}/url [get]
func (h *Handler) GetFileURL(c *fiber.Ctx) error {
	orgID, _, err := parseTenantOrgID(c)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := h.service.GetFileURL(ctx, orgID, fileID)
	if err != nil {
		switch {
		case errors.Is(err, platformmedia.ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp ảnh thuộc Đơn vị này", nil)
		case errors.Is(err, platformmedia.ErrStorageNotConfigured):
			return response.Error(c, fiber.StatusServiceUnavailable, response.ErrCodeStorageNotConfigured, "Dịch vụ lưu trữ S3/R2 chưa được cấu hình", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy đường dẫn ảnh", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, out)
}

// DeleteFile godoc
// @Summary [Hạn chế sử dụng] Chuyển tệp của Đơn vị vào thùng rác theo file_id
// @Description Hạn chế sử dụng — ưu tiên dùng DELETE /v1/media/by-url với access_url.
// @Deprecated
// @Tags Tenant Media
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/media/{file_id} [delete]
func (h *Handler) DeleteFile(c *fiber.Ctx) error {
	c.Set("Deprecation", "true")
	orgID, _, err := parseTenantOrgID(c)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = h.service.DeleteFileForActor(ctx, orgID, fileID, getTenantActorUUID(c), "TENANT", c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, platformmedia.ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp thuộc Đơn vị này", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể chuyển tệp vào thùng rác", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{
		"deleted_file_id": fileID,
		"retention_days":  platformmedia.TrashRetentionDays(),
	})
}

// DeleteFileByURL godoc
// @Summary Xóa tệp của Đơn vị theo access_url (chuyển vào thùng rác, hoàn dung lượng DB, tự xóa khỏi R2 sau N ngày)
// @Tags Tenant Media
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param body body media.DeleteByURLInput true "Đường dẫn access_url của tệp cần xóa"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/media/by-url [delete]
func (h *Handler) DeleteFileByURL(c *fiber.Ctx) error {
	orgID, _, err := parseTenantOrgID(c)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	var body platformmedia.DeleteByURLInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fileID, objectKey, err := h.service.DeleteFileByAccessURLForActor(ctx, orgID, body.AccessURL, getTenantActorUUID(c), "TENANT", c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, platformmedia.ErrInvalidAccessURL):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, platformmedia.ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp thuộc Đơn vị này", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa tệp theo access_url", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{
		"deleted_file_id": fileID,
		"object_key":      objectKey,
		"retention_days":  platformmedia.TrashRetentionDays(),
	})
}

// ListTrash godoc
// @Summary Danh sách tệp đang nằm trong thùng rác của Đơn vị hiện tại
// @Tags Tenant Media
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} response.SuccessListResponse{data=[]media.StoredFile}
// @Failure 403 {object} response.ErrorResponse
// @Router /v1/media/trash [get]
func (h *Handler) ListTrash(c *fiber.Ctx) error {
	orgID, _, err := parseTenantOrgID(c)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	files, total, err := h.service.ListTrash(ctx, orgID, page, limit)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách thùng rác", err.Error())
	}

	return response.SuccessWithMeta(c, fiber.StatusOK, files, response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// RestoreFile godoc
// @Summary Khôi phục tệp của Đơn vị từ thùng rác (trước khi bị dọn khỏi R2)
// @Tags Tenant Media
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse{data=media.StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 413 {object} response.ErrorResponse
// @Router /v1/media/{file_id}/restore [post]
func (h *Handler) RestoreFile(c *fiber.Ctx) error {
	orgID, _, err := parseTenantOrgID(c)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	file, err := h.service.RestoreFileForActor(ctx, orgID, fileID, getTenantActorUUID(c), "TENANT", c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, platformmedia.ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp trong thùng rác (hoặc đã bị xóa vĩnh viễn)", nil)
		case errors.Is(err, platformmedia.ErrStorageQuotaExceeded):
			return response.Error(c, fiber.StatusRequestEntityTooLarge, response.ErrCodeStorageQuotaExceeded, "Không đủ dung lượng gói dịch vụ để khôi phục tệp này", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể khôi phục tệp", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, file)
}

// PurgeFile godoc
// @Summary Xóa vĩnh viễn tệp trong thùng rác của Đơn vị khỏi R2/S3 ngay lập tức
// @Tags Tenant Media
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Subdomain header string false "Subdomain của Đơn vị (tùy chọn, mặc định tự lấy org_id từ JWT)"
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 403 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 503 {object} response.ErrorResponse
// @Router /v1/media/{file_id}/permanent [delete]
func (h *Handler) PurgeFile(c *fiber.Ctx) error {
	orgID, _, err := parseTenantOrgID(c)
	if err != nil {
		return response.Error(c, fiber.StatusUnauthorized, response.ErrCodeUnauthorized, "Thiếu ngữ cảnh Đơn vị", nil)
	}

	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.service.PurgeFileForActor(ctx, orgID, fileID, getTenantActorUUID(c), "TENANT", c.IP(), c.Get("User-Agent")); err != nil {
		switch {
		case errors.Is(err, platformmedia.ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp trong thùng rác", nil)
		case errors.Is(err, platformmedia.ErrStorageNotConfigured):
			return response.Error(c, fiber.StatusServiceUnavailable, response.ErrCodeStorageNotConfigured, "Dịch vụ lưu trữ S3/R2 chưa được cấu hình", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa vĩnh viễn tệp khỏi R2/S3", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"purged_file_id": fileID})
}
