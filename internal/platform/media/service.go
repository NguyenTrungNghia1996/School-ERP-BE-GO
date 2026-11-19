package media

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"school-erp/internal/pkg/storage"
	"school-erp/internal/platform/audit"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrStorageNotConfigured = errors.New("object storage is not configured")
	ErrOrgNotFound          = errors.New("organization not found")
	ErrFileNotFound         = errors.New("file not found")
	ErrInvalidImageType     = errors.New("chỉ hỗ trợ ảnh (JPEG, PNG, WEBP, GIF) và tài liệu (PDF, Word DOC/DOCX, Excel XLS/XLSX)")
	ErrInvalidFileType      = ErrInvalidImageType
	ErrFileTooLarge         = errors.New("tệp tải lên vượt quá dung lượng tối đa cho phép")
	ErrInvalidFolder        = errors.New("invalid folder name")
	ErrSchoolNotFound       = errors.New("school not found in organization")
	ErrStorageQuotaExceeded = errors.New("organization storage quota exceeded")

	folderRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9/_-]{0,79}$`)

	ole2MagicHeader = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

	allowedImageExts = map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/webp": ".webp",
		"image/gif":  ".gif",
	}
)

const (
	defaultMaxFileBytes    int64 = 25 * 1024 * 1024       // 25 MB
	defaultOrgQuotaBytes   int64 = 1 * 1024 * 1024 * 1024 // 1 GB fallback if package has no quota
	defaultPresignDuration       = 15 * time.Minute
)

// QuotaProvider resolves an organization's effective storage limit in bytes.
type QuotaProvider interface {
	GetStorageQuotaBytes(ctx context.Context, orgID uuid.UUID) (int64, error)
}

type Service struct {
	db      *gorm.DB
	store   storage.ObjectStorage
	quotas  QuotaProvider
	auditor *audit.Service
}

func NewService(db *gorm.DB, store storage.ObjectStorage, quotas QuotaProvider, auditor *audit.Service) *Service {
	return &Service{
		db:      db,
		store:   store,
		quotas:  quotas,
		auditor: auditor,
	}
}

// MaxImageSizeBytes returns the maximum allowed image payload size in bytes.
func MaxImageSizeBytes() int64 {
	return maxImageSizeBytes()
}

func maxImageSizeBytes() int64 {
	for _, envKey := range []string{"STORAGE_MAX_FILE_BYTES", "STORAGE_MAX_IMAGE_BYTES"} {
		if raw := strings.TrimSpace(os.Getenv(envKey)); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
				return v
			}
		}
	}
	return defaultMaxFileBytes
}

func fallbackQuotaBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv("STORAGE_DEFAULT_QUOTA_BYTES")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return defaultOrgQuotaBytes
}

// SanitizeFolder normalizes and validates a sub-folder path inside an organization's root directory.
func SanitizeFolder(folder string) (string, error) {
	f := strings.ToLower(strings.Trim(strings.TrimSpace(folder), "/"))
	if f == "" {
		return "general", nil
	}
	if strings.Contains(f, "..") || strings.Contains(f, "//") || !folderRegex.MatchString(f) {
		return "", ErrInvalidFolder
	}
	return f, nil
}

// BuildOrgObjectKey builds a tenant-isolated S3/R2 object key under orgs/{org_id}/...
func BuildOrgObjectKey(orgID uuid.UUID, schoolID *uuid.UUID, folder string, fileID uuid.UUID, ext string, now time.Time) string {
	if schoolID != nil && *schoolID != uuid.Nil {
		return fmt.Sprintf("orgs/%s/schools/%s/%s/%04d/%02d/%s%s",
			orgID.String(), schoolID.String(), folder, now.Year(), int(now.Month()), fileID.String(), ext)
	}
	return fmt.Sprintf("orgs/%s/%s/%04d/%02d/%s%s",
		orgID.String(), folder, now.Year(), int(now.Month()), fileID.String(), ext)
}

// ValidateImagePayload checks size and detects MIME type from actual file bytes (kept for compatibility).
func ValidateImagePayload(data []byte) (string, string, error) {
	return ValidateFilePayload(data, "")
}

// ValidateFilePayload checks size and detects MIME type from actual file bytes.
// Supports images (JPEG, PNG, WEBP, GIF) and documents (PDF, Word DOC/DOCX, Excel XLS/XLSX).
func ValidateFilePayload(data []byte, originalName string) (string, string, error) {
	if len(data) == 0 {
		return "", "", ErrInvalidFileType
	}
	if int64(len(data)) > maxImageSizeBytes() {
		return "", "", ErrFileTooLarge
	}

	mimeType := http.DetectContentType(data)
	if ext, ok := allowedImageExts[mimeType]; ok {
		return mimeType, ext, nil
	}

	if mimeType == "application/pdf" || bytes.HasPrefix(data, []byte("%PDF-")) {
		return "application/pdf", ".pdf", nil
	}

	// OOXML (DOCX / XLSX) are ZIP archives containing [Content_Types].xml and word/ or xl/ XML entries
	if mimeType == "application/zip" || bytes.HasPrefix(data, []byte{'P', 'K', 0x03, 0x04}) {
		if ooxmlMime, ooxmlExt, ok := detectOOXML(data); ok {
			return ooxmlMime, ooxmlExt, nil
		}
		return "", "", ErrInvalidFileType
	}

	// Legacy Microsoft Office OLE2 binary format (.doc / .xls)
	if bytes.HasPrefix(data, ole2MagicHeader) {
		switch strings.ToLower(filepath.Ext(strings.TrimSpace(originalName))) {
		case ".doc":
			return "application/msword", ".doc", nil
		case ".xls":
			return "application/vnd.ms-excel", ".xls", nil
		}
	}

	return "", "", ErrInvalidFileType
}

func detectOOXML(data []byte) (string, string, bool) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "", false
	}
	var hasContentTypes, hasWord, hasExcel bool
	for _, f := range zr.File {
		switch f.Name {
		case "[Content_Types].xml":
			hasContentTypes = true
		case "word/document.xml":
			hasWord = true
		case "xl/workbook.xml":
			hasExcel = true
		}
	}
	if !hasContentTypes {
		return "", "", false
	}
	if hasWord {
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx", true
	}
	if hasExcel {
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx", true
	}
	return "", "", false
}

type UploadImageInput struct {
	OrganizationID uuid.UUID
	SchoolID       *uuid.UUID
	UploadedBy     uuid.UUID
	ActorType      string // "PLATFORM" (default) | "TENANT"
	Folder         string
	OriginalName   string
	Data           []byte
	IPAddress      string
	UserAgent      string
}

func (s *Service) resolveOrgQuotaBytes(ctx context.Context, orgID uuid.UUID) int64 {
	if s.quotas != nil {
		if q, err := s.quotas.GetStorageQuotaBytes(ctx, orgID); err == nil && q > 0 {
			return q
		}
	}
	return fallbackQuotaBytes()
}

func (s *Service) ensureOrgExists(ctx context.Context, orgID uuid.UUID) error {
	var count int64
	if err := s.db.WithContext(ctx).Table("organizations").
		Where("id = ? AND deleted_at IS NULL", orgID).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrOrgNotFound
	}
	return nil
}

func (s *Service) UploadImage(ctx context.Context, input UploadImageInput) (*StoredFile, error) {
	if s.store == nil || !s.store.IsConfigured() {
		return nil, ErrStorageNotConfigured
	}

	mimeType, ext, err := ValidateFilePayload(input.Data, input.OriginalName)
	if err != nil {
		return nil, err
	}

	folder, err := SanitizeFolder(input.Folder)
	if err != nil {
		return nil, err
	}

	if err := s.ensureOrgExists(ctx, input.OrganizationID); err != nil {
		return nil, err
	}
	if input.SchoolID != nil && *input.SchoolID != uuid.Nil {
		var schoolCount int64
		if err := s.db.WithContext(ctx).Table("schools").
			Where("id = ? AND organization_id = ? AND deleted_at IS NULL", *input.SchoolID, input.OrganizationID).
			Count(&schoolCount).Error; err != nil {
			return nil, err
		}
		if schoolCount == 0 {
			return nil, ErrSchoolNotFound
		}
	}

	fileSize := int64(len(input.Data))
	quotaBytes := s.resolveOrgQuotaBytes(ctx, input.OrganizationID)

	// 1. Pre-check quota with row lock before uploading to R2/S3
	if err := s.checkQuotaAvailable(ctx, input.OrganizationID, fileSize, quotaBytes); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	fileID := uuid.New()
	objectKey := BuildOrgObjectKey(input.OrganizationID, input.SchoolID, folder, fileID, ext, now)

	// 2. Upload object to S3 / Cloudflare R2
	if err := s.store.PutObject(ctx, objectKey, mimeType, input.Data); err != nil {
		return nil, err
	}

	var publicURLPtr *string
	if pub := s.store.PublicURL(objectKey); pub != "" {
		publicURLPtr = &pub
	}

	cleanName := filepath.Base(strings.TrimSpace(input.OriginalName))
	if cleanName == "" || cleanName == "." {
		cleanName = fileID.String() + ext
	}

	record := &StoredFile{
		ID:              fileID,
		OrganizationID:  input.OrganizationID,
		SchoolID:        input.SchoolID,
		UploadedBy:      &input.UploadedBy,
		Folder:          folder,
		ObjectKey:       objectKey,
		OriginalName:    cleanName,
		MimeType:        mimeType,
		SizeBytes:       fileSize,
		StorageProvider: s.store.Provider(),
		Bucket:          s.store.Bucket(),
		PublicURL:       publicURLPtr,
		CreatedAt:       now,
	}

	// 3. Persist file metadata and atomically increment org storage usage
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			INSERT INTO organization_storage_usages (organization_id, used_bytes, file_count, updated_at)
			VALUES (?, 0, 0, NOW())
			ON CONFLICT (organization_id) DO NOTHING
		`, input.OrganizationID).Error; err != nil {
			return err
		}

		var usage OrganizationStorageUsage
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&usage, "organization_id = ?", input.OrganizationID).Error; err != nil {
			return err
		}

		if usage.UsedBytes+fileSize > quotaBytes {
			return ErrStorageQuotaExceeded
		}

		if err := tx.Create(record).Error; err != nil {
			return err
		}

		if err := tx.Model(&OrganizationStorageUsage{}).
			Where("organization_id = ?", input.OrganizationID).
			Updates(map[string]any{
				"used_bytes": gorm.Expr("used_bytes + ?", fileSize),
				"file_count": gorm.Expr("file_count + 1"),
				"updated_at": now,
			}).Error; err != nil {
			return err
		}

		if s.auditor != nil {
			actorType := strings.ToUpper(strings.TrimSpace(input.ActorType))
			if actorType == "" {
				actorType = "PLATFORM"
			}
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      actorType,
				ActorID:        &input.UploadedBy,
				OrganizationID: &input.OrganizationID,
				Action:         "MEDIA_IMAGE_UPLOADED",
				ResourceType:   "STORED_FILE",
				ResourceID:     &record.ID,
				AfterData: map[string]any{
					"object_key": objectKey,
					"size_bytes": fileSize,
					"mime_type":  mimeType,
					"folder":     folder,
				},
				IPAddress: input.IPAddress,
				UserAgent: input.UserAgent,
			})
		}
		return nil
	})
	if err != nil {
		// Clean up uploaded object so S3/R2 doesn't retain orphan files
		_ = s.store.DeleteObject(context.Background(), objectKey)
		return nil, err
	}

	record.AccessURL = s.resolveAccessURL(ctx, record)
	return record, nil
}

func (s *Service) checkQuotaAvailable(ctx context.Context, orgID uuid.UUID, fileSize, quotaBytes int64) error {
	var usage OrganizationStorageUsage
	err := s.db.WithContext(ctx).First(&usage, "organization_id = ?", orgID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if fileSize > quotaBytes {
			return ErrStorageQuotaExceeded
		}
		return nil
	}
	if err != nil {
		return err
	}
	if usage.UsedBytes+fileSize > quotaBytes {
		return ErrStorageQuotaExceeded
	}
	return nil
}

func (s *Service) GetUsage(ctx context.Context, orgID uuid.UUID) (*StorageUsageOutput, error) {
	if err := s.ensureOrgExists(ctx, orgID); err != nil {
		return nil, err
	}

	var usage OrganizationStorageUsage
	err := s.db.WithContext(ctx).First(&usage, "organization_id = ?", orgID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	quotaBytes := s.resolveOrgQuotaBytes(ctx, orgID)
	remaining := quotaBytes - usage.UsedBytes
	if remaining < 0 {
		remaining = 0
	}

	var pct float64
	if quotaBytes > 0 {
		pct = float64(usage.UsedBytes) * 100 / float64(quotaBytes)
	}

	return &StorageUsageOutput{
		OrganizationID: orgID,
		FolderPrefix:   fmt.Sprintf("orgs/%s/", orgID.String()),
		UsedBytes:      usage.UsedBytes,
		QuotaBytes:     quotaBytes,
		RemainingBytes: remaining,
		UsagePercent:   pct,
		FileCount:      usage.FileCount,
	}, nil
}

func (s *Service) ListFiles(ctx context.Context, orgID uuid.UUID, folder string, page, limit int) ([]StoredFile, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	q := s.db.WithContext(ctx).Model(&StoredFile{}).Where("organization_id = ?", orgID)
	if strings.TrimSpace(folder) != "" {
		cleanFolder, err := SanitizeFolder(folder)
		if err != nil {
			return nil, 0, err
		}
		q = q.Where("folder = ?", cleanFolder)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var files []StoredFile
	offset := (page - 1) * limit
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&files).Error; err != nil {
		return nil, 0, err
	}

	for i := range files {
		files[i].AccessURL = s.resolveAccessURL(ctx, &files[i])
	}
	return files, total, nil
}

func (s *Service) GetFileURL(ctx context.Context, orgID, fileID uuid.UUID) (*MediaFileURLOutput, error) {
	var file StoredFile
	if err := s.db.WithContext(ctx).First(&file, "id = ? AND organization_id = ?", fileID, orgID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}

	if file.PublicURL != nil && *file.PublicURL != "" {
		return &MediaFileURLOutput{
			FileID:    file.ID,
			ObjectKey: file.ObjectKey,
			URL:       *file.PublicURL,
		}, nil
	}

	if s.store == nil || !s.store.IsConfigured() {
		return nil, ErrStorageNotConfigured
	}
	url, err := s.store.PresignGetURL(ctx, file.ObjectKey, defaultPresignDuration)
	if err != nil {
		return nil, err
	}
	return &MediaFileURLOutput{
		FileID:    file.ID,
		ObjectKey: file.ObjectKey,
		URL:       url,
		ExpiresIn: int(defaultPresignDuration.Seconds()),
	}, nil
}

func (s *Service) DeleteFile(ctx context.Context, orgID, fileID, actorID uuid.UUID, ip, ua string) error {
	return s.DeleteFileForActor(ctx, orgID, fileID, actorID, "PLATFORM", ip, ua)
}

func (s *Service) DeleteFileForActor(ctx context.Context, orgID, fileID, actorID uuid.UUID, actorType, ip, ua string) error {
	if s.store == nil || !s.store.IsConfigured() {
		return ErrStorageNotConfigured
	}
	actorType = strings.ToUpper(strings.TrimSpace(actorType))
	if actorType == "" {
		actorType = "PLATFORM"
	}

	var file StoredFile
	if err := s.db.WithContext(ctx).First(&file, "id = ? AND organization_id = ?", fileID, orgID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrFileNotFound
		}
		return err
	}

	if err := s.store.DeleteObject(ctx, file.ObjectKey); err != nil {
		return err
	}

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&file).Error; err != nil {
			return err
		}

		if err := tx.Model(&OrganizationStorageUsage{}).
			Where("organization_id = ?", orgID).
			Updates(map[string]any{
				"used_bytes": gorm.Expr("GREATEST(0, used_bytes - ?)", file.SizeBytes),
				"file_count": gorm.Expr("GREATEST(0, file_count - 1)"),
				"updated_at": now,
			}).Error; err != nil {
			return err
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      actorType,
				ActorID:        &actorID,
				OrganizationID: &orgID,
				Action:         "MEDIA_IMAGE_DELETED",
				ResourceType:   "STORED_FILE",
				ResourceID:     &file.ID,
				BeforeData: map[string]any{
					"object_key": file.ObjectKey,
					"size_bytes": file.SizeBytes,
				},
				IPAddress: ip,
				UserAgent: ua,
			})
		}
		return nil
	})
}

func (s *Service) resolveAccessURL(ctx context.Context, file *StoredFile) string {
	if file.PublicURL != nil && *file.PublicURL != "" {
		return *file.PublicURL
	}
	if s.store == nil || !s.store.IsConfigured() {
		return ""
	}
	url, err := s.store.PresignGetURL(ctx, file.ObjectKey, defaultPresignDuration)
	if err != nil {
		return ""
	}
	return url
}
