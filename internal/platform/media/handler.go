package media

import (
	"context"
	"errors"
	"io"
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

// UploadImage godoc
// @Summary Upload ảnh hoặc tài liệu (PDF, Word, Excel) vào thư mục riêng của Tổ chức trên S3/R2
// @Tags Platform Media
// @Accept mpfd
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param file formData file true "Tệp ảnh (JPEG, PNG, WEBP, GIF) hoặc tài liệu (PDF, DOC, DOCX, XLS, XLSX; tối đa 25 MB)"
// @Param folder formData string false "Thư mục con (VD: logos, avatars, students, documents)"
// @Param school_id formData string false "School ID (nếu tách theo trường)"
// @Success 201 {object} response.SuccessResponse{data=StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 413 {object} response.ErrorResponse
// @Failure 503 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/media/upload [post]
func (h *Handler) UploadImage(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Thiếu tệp ảnh (field 'file')", nil)
	}

	var schoolIDPtr *uuid.UUID
	if rawSchoolID := strings.TrimSpace(c.FormValue("school_id")); rawSchoolID != "" {
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

	data, err := io.ReadAll(io.LimitReader(f, maxImageSizeBytes()+1))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Lỗi đọc dữ liệu tệp ảnh", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	record, err := h.service.UploadImage(ctx, UploadImageInput{
		OrganizationID: orgID,
		SchoolID:       schoolIDPtr,
		UploadedBy:     getActorUUID(c),
		Folder:         c.FormValue("folder"),
		OriginalName:   fh.Filename,
		Data:           data,
		IPAddress:      c.IP(),
		UserAgent:      c.Get("User-Agent"),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrStorageNotConfigured):
			return response.Error(c, fiber.StatusServiceUnavailable, response.ErrCodeStorageNotConfigured, "Dịch vụ lưu trữ S3/R2 chưa được cấu hình", nil)
		case errors.Is(err, ErrOrgNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Tổ chức", nil)
		case errors.Is(err, ErrSchoolNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Trường học thuộc Tổ chức này", nil)
		case errors.Is(err, ErrInvalidImageType), errors.Is(err, ErrInvalidFolder):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, ErrFileTooLarge):
			return response.Error(c, fiber.StatusRequestEntityTooLarge, response.ErrCodeValidationFailed, "Kích thước ảnh vượt quá giới hạn cho phép", nil)
		case errors.Is(err, ErrStorageQuotaExceeded):
			return response.Error(c, fiber.StatusRequestEntityTooLarge, response.ErrCodeStorageQuotaExceeded, "Dung lượng lưu trữ của đơn vị đã đạt hạn mức gói dịch vụ", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải ảnh lên S3/R2", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, record)
}

// GetUsage godoc
// @Summary Thống kê dung lượng lưu trữ ảnh của Tổ chức
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Success 200 {object} response.SuccessResponse{data=StorageUsageOutput}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/storage/usage [get]
func (h *Handler) GetUsage(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	usage, err := h.service.GetUsage(ctx, orgID)
	if err != nil {
		if errors.Is(err, ErrOrgNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy Tổ chức", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy thống kê dung lượng", err.Error())
	}

	return response.Success(c, fiber.StatusOK, usage)
}

// ListFiles godoc
// @Summary Danh sách ảnh đã tải lên của Tổ chức
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param folder query string false "Lọc theo thư mục con"
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} response.SuccessListResponse{data=[]StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/media [get]
func (h *Handler) ListFiles(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	files, total, err := h.service.ListFiles(ctx, orgID, c.Query("folder"), page, limit)
	if err != nil {
		if errors.Is(err, ErrInvalidFolder) {
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
// @Summary Lấy đường dẫn truy cập (Public hoặc Presigned URL) của tệp ảnh
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse{data=MediaFileURLOutput}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/media/{file_id}/url [get]
func (h *Handler) GetFileURL(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
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
		case errors.Is(err, ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp ảnh thuộc đơn vị này", nil)
		case errors.Is(err, ErrStorageNotConfigured):
			return response.Error(c, fiber.StatusServiceUnavailable, response.ErrCodeStorageNotConfigured, "Dịch vụ lưu trữ S3/R2 chưa được cấu hình", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy đường dẫn ảnh", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, out)
}

// DeleteFile godoc
// @Summary [Hạn chế sử dụng] Chuyển tệp vào thùng rác theo file_id
// @Description Hạn chế sử dụng — ưu tiên dùng DELETE /v1/platform/organizations/{org_id}/media/by-url với access_url.
// @Deprecated
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/media/{file_id} [delete]
func (h *Handler) DeleteFile(c *fiber.Ctx) error {
	c.Set("Deprecation", "true")
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}
	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = h.service.DeleteFile(ctx, orgID, fileID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp thuộc đơn vị này", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể chuyển tệp vào thùng rác", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{
		"deleted_file_id": fileID,
		"retention_days":  TrashRetentionDays(),
	})
}


// DeleteFileByURL godoc
// @Summary Xóa tệp theo access_url (chuyển vào thùng rác, hoàn dung lượng DB, tự xóa khỏi R2 sau N ngày)
// @Tags Platform Media
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param body body DeleteByURLInput true "Đường dẫn access_url của tệp cần xóa"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/media/by-url [delete]
func (h *Handler) DeleteFileByURL(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}

	var body DeleteByURLInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fileID, objectKey, err := h.service.DeleteFileByAccessURLForActor(ctx, orgID, body.AccessURL, getActorUUID(c), "PLATFORM", c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidAccessURL):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp thuộc đơn vị này", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa tệp theo access_url", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{
		"deleted_file_id": fileID,
		"object_key":      objectKey,
		"retention_days":  TrashRetentionDays(),
	})
}

// ListTrash godoc
// @Summary Danh sách tệp đang nằm trong thùng rác của Tổ chức (chưa bị dọn khỏi R2)
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} response.SuccessListResponse{data=[]StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/media/trash [get]
func (h *Handler) ListTrash(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
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
// @Summary Khôi phục tệp từ thùng rác (trước khi bị dọn khỏi R2)
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse{data=StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 413 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/media/{file_id}/restore [post]
func (h *Handler) RestoreFile(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}
	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	file, err := h.service.RestoreFileForActor(ctx, orgID, fileID, getActorUUID(c), "PLATFORM", c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp trong thùng rác (hoặc đã bị xóa vĩnh viễn)", nil)
		case errors.Is(err, ErrStorageQuotaExceeded):
			return response.Error(c, fiber.StatusRequestEntityTooLarge, response.ErrCodeStorageQuotaExceeded, "Không đủ dung lượng gói dịch vụ để khôi phục tệp này", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể khôi phục tệp", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, file)
}

// PurgeFile godoc
// @Summary Xóa vĩnh viễn tệp trong thùng rác khỏi R2/S3 ngay lập tức
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param org_id path string true "Organization ID"
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 503 {object} response.ErrorResponse
// @Router /v1/platform/organizations/{org_id}/media/{file_id}/permanent [delete]
func (h *Handler) PurgeFile(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("org_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID Tổ chức không hợp lệ", nil)
	}
	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.service.PurgeFileForActor(ctx, orgID, fileID, getActorUUID(c), "PLATFORM", c.IP(), c.Get("User-Agent")); err != nil {
		switch {
		case errors.Is(err, ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp trong thùng rác", nil)
		case errors.Is(err, ErrStorageNotConfigured):
			return response.Error(c, fiber.StatusServiceUnavailable, response.ErrCodeStorageNotConfigured, "Dịch vụ lưu trữ S3/R2 chưa được cấu hình", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa vĩnh viễn tệp khỏi R2/S3", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"purged_file_id": fileID})
}

// UploadPlatformFile godoc
// @Summary Upload ảnh hoặc tài liệu cấp Platform (không cần org_id, lưu tại platform/{folder}/...)
// @Tags Platform Media
// @Accept mpfd
// @Produce json
// @Security BearerAuth
// @Param file formData file true "Tệp ảnh (JPEG, PNG, WEBP, GIF) hoặc tài liệu (PDF, DOC, DOCX, XLS, XLSX; tối đa 25 MB)"
// @Param folder formData string false "Thư mục con (VD: avatars, branding, docs; mặc định: general)"
// @Success 201 {object} response.SuccessResponse{data=StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Failure 413 {object} response.ErrorResponse
// @Failure 503 {object} response.ErrorResponse
// @Router /v1/platform/media/upload [post]
func (h *Handler) UploadPlatformFile(c *fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Thiếu tệp tải lên (field 'file')", nil)
	}

	f, err := fh.Open()
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Không thể đọc tệp tải lên", err.Error())
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxImageSizeBytes()+1))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Lỗi đọc dữ liệu tệp", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	record, err := h.service.UploadPlatformFile(ctx, UploadPlatformFileInput{
		UploadedBy:   getActorUUID(c),
		Folder:       c.FormValue("folder"),
		OriginalName: fh.Filename,
		Data:         data,
		IPAddress:    c.IP(),
		UserAgent:    c.Get("User-Agent"),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrStorageNotConfigured):
			return response.Error(c, fiber.StatusServiceUnavailable, response.ErrCodeStorageNotConfigured, "Dịch vụ lưu trữ S3/R2 chưa được cấu hình", nil)
		case errors.Is(err, ErrInvalidImageType), errors.Is(err, ErrInvalidFolder):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, ErrFileTooLarge):
			return response.Error(c, fiber.StatusRequestEntityTooLarge, response.ErrCodeValidationFailed, "Kích thước tệp vượt quá giới hạn cho phép", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải tệp lên S3/R2", err.Error())
		}
	}

	return response.Success(c, fiber.StatusCreated, record)
}

// ListPlatformFiles godoc
// @Summary Danh sách tệp media cấp Platform (không gắn org_id)
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param folder query string false "Lọc theo thư mục con"
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} response.SuccessListResponse{data=[]StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Router /v1/platform/media [get]
func (h *Handler) ListPlatformFiles(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	files, total, err := h.service.ListPlatformFiles(ctx, c.Query("folder"), page, limit)
	if err != nil {
		if errors.Is(err, ErrInvalidFolder) {
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Tên thư mục không hợp lệ", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách tệp Platform", err.Error())
	}

	return response.SuccessWithMeta(c, fiber.StatusOK, files, response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// DeletePlatformFileByURL godoc
// @Summary Xóa tệp cấp Platform theo access_url (chuyển vào thùng rác, tự xóa khỏi R2 sau N ngày)
// @Tags Platform Media
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body DeleteByURLInput true "Đường dẫn access_url của tệp cấp Platform cần xóa"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/media/by-url [delete]
func (h *Handler) DeletePlatformFileByURL(c *fiber.Ctx) error {
	var body DeleteByURLInput
	if err := c.BodyParser(&body); err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "Dữ liệu không hợp lệ", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fileID, objectKey, err := h.service.DeletePlatformFileByAccessURL(ctx, body.AccessURL, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidAccessURL):
			return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, err.Error(), nil)
		case errors.Is(err, ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp cấp Platform", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa tệp theo access_url", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{
		"deleted_file_id": fileID,
		"object_key":      objectKey,
		"retention_days":  TrashRetentionDays(),
	})
}

// DeletePlatformFile godoc
// @Summary [Hạn chế sử dụng] Chuyển tệp cấp Platform vào thùng rác theo file_id
// @Description Hạn chế sử dụng — ưu tiên dùng DELETE /v1/platform/media/by-url với access_url.
// @Deprecated
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/media/{file_id} [delete]
func (h *Handler) DeletePlatformFile(c *fiber.Ctx) error {
	c.Set("Deprecation", "true")
	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.service.DeletePlatformFile(ctx, fileID, getActorUUID(c), c.IP(), c.Get("User-Agent")); err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp cấp Platform", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể chuyển tệp vào thùng rác", err.Error())
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{
		"deleted_file_id": fileID,
		"retention_days":  TrashRetentionDays(),
	})
}

// GetPlatformFileURL godoc
// @Summary Lấy đường dẫn truy cập (Public hoặc Presigned URL) của tệp cấp Platform
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse{data=MediaFileURLOutput}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/media/{file_id}/url [get]
func (h *Handler) GetPlatformFileURL(c *fiber.Ctx) error {
	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := h.service.GetPlatformFileURL(ctx, fileID)
	if err != nil {
		switch {
		case errors.Is(err, ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp cấp Platform", nil)
		case errors.Is(err, ErrStorageNotConfigured):
			return response.Error(c, fiber.StatusServiceUnavailable, response.ErrCodeStorageNotConfigured, "Dịch vụ lưu trữ S3/R2 chưa được cấu hình", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể lấy đường dẫn tệp", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, out)
}

// ListPlatformTrash godoc
// @Summary Danh sách tệp cấp Platform đang nằm trong thùng rác
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param page query int false "Trang"
// @Param limit query int false "Số lượng mỗi trang"
// @Success 200 {object} response.SuccessListResponse{data=[]StoredFile}
// @Router /v1/platform/media/trash [get]
func (h *Handler) ListPlatformTrash(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	files, total, err := h.service.ListPlatformTrash(ctx, page, limit)
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể tải danh sách thùng rác Platform", err.Error())
	}

	return response.SuccessWithMeta(c, fiber.StatusOK, files, response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// RestorePlatformFile godoc
// @Summary Khôi phục tệp cấp Platform từ thùng rác
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse{data=StoredFile}
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Router /v1/platform/media/{file_id}/restore [post]
func (h *Handler) RestorePlatformFile(c *fiber.Ctx) error {
	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	file, err := h.service.RestorePlatformFile(ctx, fileID, getActorUUID(c), c.IP(), c.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp trong thùng rác Platform", nil)
		}
		return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể khôi phục tệp", err.Error())
	}

	return response.Success(c, fiber.StatusOK, file)
}

// PurgePlatformFile godoc
// @Summary Xóa vĩnh viễn tệp trong thùng rác cấp Platform khỏi R2/S3 ngay lập tức
// @Tags Platform Media
// @Produce json
// @Security BearerAuth
// @Param file_id path string true "File ID"
// @Success 200 {object} response.SuccessResponse
// @Failure 400 {object} response.ErrorResponse
// @Failure 404 {object} response.ErrorResponse
// @Failure 503 {object} response.ErrorResponse
// @Router /v1/platform/media/{file_id}/permanent [delete]
func (h *Handler) PurgePlatformFile(c *fiber.Ctx) error {
	fileID, err := uuid.Parse(c.Params("file_id"))
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, response.ErrCodeValidationFailed, "ID tệp không hợp lệ", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.service.PurgePlatformFile(ctx, fileID, getActorUUID(c), c.IP(), c.Get("User-Agent")); err != nil {
		switch {
		case errors.Is(err, ErrFileNotFound):
			return response.Error(c, fiber.StatusNotFound, response.ErrCodeNotFound, "Không tìm thấy tệp trong thùng rác Platform", nil)
		case errors.Is(err, ErrStorageNotConfigured):
			return response.Error(c, fiber.StatusServiceUnavailable, response.ErrCodeStorageNotConfigured, "Dịch vụ lưu trữ S3/R2 chưa được cấu hình", nil)
		default:
			return response.Error(c, fiber.StatusInternalServerError, response.ErrCodeInternalError, "Không thể xóa vĩnh viễn tệp", err.Error())
		}
	}

	return response.Success(c, fiber.StatusOK, fiber.Map{"purged_file_id": fileID})
}
