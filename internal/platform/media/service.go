package media

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	ErrInvalidAccessURL     = errors.New("access_url không hợp lệ")
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
	defaultMaxFileBytes       int64 = 25 * 1024 * 1024       // 25 MB
	defaultOrgQuotaBytes      int64 = 1 * 1024 * 1024 * 1024 // 1 GB fallback if package has no quota
	defaultPresignDuration          = 15 * time.Minute
	defaultTrashRetentionDays       = 30
	defaultPurgeInterval            = 60 * time.Minute
	defaultPurgeBatchSize           = 100
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

// TrashRetentionDays returns how many days trashed files are kept on R2 before permanent purge.
func TrashRetentionDays() int {
	if raw := strings.TrimSpace(os.Getenv("STORAGE_TRASH_RETENTION_DAYS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return defaultTrashRetentionDays
}

func purgeInterval() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("STORAGE_PURGE_INTERVAL_MINUTES")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return time.Duration(v) * time.Minute
		}
	}
	return defaultPurgeInterval
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

// BuildPlatformObjectKey builds a platform-level S3/R2 object key under platform/{folder}/{YYYY}/{MM}/{file_id}{ext}.
func BuildPlatformObjectKey(folder string, fileID uuid.UUID, ext string, now time.Time) string {
	return fmt.Sprintf("platform/%s/%04d/%02d/%s%s",
		folder, now.Year(), int(now.Month()), fileID.String(), ext)
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

	orgIDCopy := input.OrganizationID
	record := &StoredFile{
		ID:              fileID,
		OrganizationID:  &orgIDCopy,
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

// ExtractObjectKeyFromAccessURL extracts the canonical tenant object_key ("orgs/...") from a public or presigned URL.
func ExtractObjectKeyFromAccessURL(accessURL string) (string, error) {
	raw := strings.TrimSpace(accessURL)
	if raw == "" {
		return "", ErrInvalidAccessURL
	}

	pathPart := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", ErrInvalidAccessURL
		}
		pathPart = u.Path
	} else {
		if idx := strings.IndexAny(pathPart, "?#"); idx >= 0 {
			pathPart = pathPart[:idx]
		}
	}

	if decoded, err := url.PathUnescape(pathPart); err == nil {
		pathPart = decoded
	}

	idx := strings.Index(pathPart, "orgs/")
	if idx < 0 {
		return "", ErrInvalidAccessURL
	}
	objectKey := strings.TrimLeft(pathPart[idx:], "/")
	if objectKey == "" || strings.Contains(objectKey, "..") {
		return "", ErrInvalidAccessURL
	}
	return objectKey, nil
}

// DeleteFileByAccessURLForActor resolves a file by its access_url (within orgID) and moves it to Trash.
func (s *Service) DeleteFileByAccessURLForActor(ctx context.Context, orgID uuid.UUID, accessURL string, actorID uuid.UUID, actorType, ip, ua string) (uuid.UUID, string, error) {
	objectKey, err := ExtractObjectKeyFromAccessURL(accessURL)
	if err != nil {
		return uuid.Nil, "", err
	}

	expectedPrefix := fmt.Sprintf("orgs/%s/", orgID.String())
	if !strings.HasPrefix(objectKey, expectedPrefix) {
		return uuid.Nil, "", ErrFileNotFound
	}

	var file StoredFile
	if err := s.db.WithContext(ctx).First(&file, "object_key = ? AND organization_id = ?", objectKey, orgID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return uuid.Nil, "", ErrFileNotFound
		}
		return uuid.Nil, "", err
	}

	if err := s.DeleteFileForActor(ctx, orgID, file.ID, actorID, actorType, ip, ua); err != nil {
		return uuid.Nil, "", err
	}
	return file.ID, file.ObjectKey, nil
}

// ExtractPlatformObjectKeyFromAccessURL extracts the canonical platform object_key ("platform/...") from an access URL.
func ExtractPlatformObjectKeyFromAccessURL(accessURL string) (string, error) {
	raw := strings.TrimSpace(accessURL)
	if raw == "" {
		return "", ErrInvalidAccessURL
	}

	pathPart := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", ErrInvalidAccessURL
		}
		pathPart = u.Path
	} else if idx := strings.IndexAny(pathPart, "?#"); idx >= 0 {
		pathPart = pathPart[:idx]
	}

	if decoded, err := url.PathUnescape(pathPart); err == nil {
		pathPart = decoded
	}

	idx := strings.Index(pathPart, "platform/")
	if idx < 0 {
		return "", ErrInvalidAccessURL
	}
	objectKey := strings.TrimLeft(pathPart[idx:], "/")
	if objectKey == "" || strings.Contains(objectKey, "..") {
		return "", ErrInvalidAccessURL
	}
	return objectKey, nil
}

type UploadPlatformFileInput struct {
	UploadedBy   uuid.UUID
	Folder       string
	OriginalName string
	Data         []byte
	IPAddress    string
	UserAgent    string
}

// UploadPlatformFile uploads a platform-level image/document (no org_id) under platform/{folder}/YYYY/MM/{file_id}{ext}.
func (s *Service) UploadPlatformFile(ctx context.Context, input UploadPlatformFileInput) (*StoredFile, error) {
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

	now := time.Now().UTC()
	fileID := uuid.New()
	objectKey := BuildPlatformObjectKey(folder, fileID, ext, now)

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

	fileSize := int64(len(input.Data))
	record := &StoredFile{
		ID:              fileID,
		OrganizationID:  nil,
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

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:    "PLATFORM",
				ActorID:      &input.UploadedBy,
				Action:       "PLATFORM_MEDIA_UPLOADED",
				ResourceType: "STORED_FILE",
				ResourceID:   &record.ID,
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
		_ = s.store.DeleteObject(context.Background(), objectKey)
		return nil, err
	}

	record.AccessURL = s.resolveAccessURL(ctx, record)
	return record, nil
}

// ListPlatformFiles lists active platform-level files (organization_id IS NULL).
func (s *Service) ListPlatformFiles(ctx context.Context, folder string, page, limit int) ([]StoredFile, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	q := s.db.WithContext(ctx).Model(&StoredFile{}).Where("organization_id IS NULL")
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

// DeletePlatformFile moves a platform-level file (organization_id IS NULL) to Trash.
func (s *Service) DeletePlatformFile(ctx context.Context, fileID, actorID uuid.UUID, ip, ua string) error {
	var file StoredFile
	if err := s.db.WithContext(ctx).First(&file, "id = ? AND organization_id IS NULL", fileID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrFileNotFound
		}
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&file).Error; err != nil {
			return err
		}
		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:    "PLATFORM",
				ActorID:      &actorID,
				Action:       "PLATFORM_MEDIA_TRASHED",
				ResourceType: "STORED_FILE",
				ResourceID:   &file.ID,
				BeforeData: map[string]any{
					"object_key":     file.ObjectKey,
					"size_bytes":     file.SizeBytes,
					"retention_days": TrashRetentionDays(),
				},
				IPAddress: ip,
				UserAgent: ua,
			})
		}
		return nil
	})
}

// DeletePlatformFileByAccessURL resolves a platform-level file by its access_url and moves it to Trash.
func (s *Service) DeletePlatformFileByAccessURL(ctx context.Context, accessURL string, actorID uuid.UUID, ip, ua string) (uuid.UUID, string, error) {
	objectKey, err := ExtractPlatformObjectKeyFromAccessURL(accessURL)
	if err != nil {
		return uuid.Nil, "", err
	}

	var file StoredFile
	if err := s.db.WithContext(ctx).First(&file, "object_key = ? AND organization_id IS NULL", objectKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return uuid.Nil, "", ErrFileNotFound
		}
		return uuid.Nil, "", err
	}

	if err := s.DeletePlatformFile(ctx, file.ID, actorID, ip, ua); err != nil {
		return uuid.Nil, "", err
	}
	return file.ID, file.ObjectKey, nil
}

// DeleteFileForActor moves a file to Trash (soft-delete in DB + frees org quota).
// The physical object on R2/S3 is retained for STORAGE_TRASH_RETENTION_DAYS before the cleanup worker purges it.
func (s *Service) DeleteFileForActor(ctx context.Context, orgID, fileID, actorID uuid.UUID, actorType, ip, ua string) error {
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
				Action:         "MEDIA_FILE_TRASHED",
				ResourceType:   "STORED_FILE",
				ResourceID:     &file.ID,
				BeforeData: map[string]any{
					"object_key":     file.ObjectKey,
					"size_bytes":     file.SizeBytes,
					"retention_days": TrashRetentionDays(),
				},
				IPAddress: ip,
				UserAgent: ua,
			})
		}
		return nil
	})
}

// ListTrash lists soft-deleted files that have not been physically purged from R2/S3 yet.
func (s *Service) ListTrash(ctx context.Context, orgID uuid.UUID, page, limit int) ([]StoredFile, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	q := s.db.WithContext(ctx).Unscoped().Model(&StoredFile{}).
		Where("organization_id = ? AND deleted_at IS NOT NULL AND purged_at IS NULL", orgID)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var files []StoredFile
	offset := (page - 1) * limit
	if err := q.Order("deleted_at DESC").Offset(offset).Limit(limit).Find(&files).Error; err != nil {
		return nil, 0, err
	}

	for i := range files {
		if files[i].DeletedAt.Valid {
			t := files[i].DeletedAt.Time
			files[i].TrashedAt = &t
		}
		files[i].AccessURL = s.resolveAccessURL(ctx, &files[i])
	}
	return files, total, nil
}

// RestoreFileForActor restores a file from Trash (before it is purged from R2) and re-checks org storage quota.
func (s *Service) RestoreFileForActor(ctx context.Context, orgID, fileID, actorID uuid.UUID, actorType, ip, ua string) (*StoredFile, error) {
	actorType = strings.ToUpper(strings.TrimSpace(actorType))
	if actorType == "" {
		actorType = "PLATFORM"
	}

	var file StoredFile
	if err := s.db.WithContext(ctx).Unscoped().
		First(&file, "id = ? AND organization_id = ? AND deleted_at IS NOT NULL AND purged_at IS NULL", fileID, orgID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}

	quotaBytes := s.resolveOrgQuotaBytes(ctx, orgID)
	now := time.Now().UTC()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			INSERT INTO organization_storage_usages (organization_id, used_bytes, file_count, updated_at)
			VALUES (?, 0, 0, NOW())
			ON CONFLICT (organization_id) DO NOTHING
		`, orgID).Error; err != nil {
			return err
		}

		var usage OrganizationStorageUsage
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&usage, "organization_id = ?", orgID).Error; err != nil {
			return err
		}
		if usage.UsedBytes+file.SizeBytes > quotaBytes {
			return ErrStorageQuotaExceeded
		}

		if err := tx.Unscoped().Model(&StoredFile{}).
			Where("id = ? AND organization_id = ?", file.ID, orgID).
			Update("deleted_at", nil).Error; err != nil {
			return err
		}

		if err := tx.Model(&OrganizationStorageUsage{}).
			Where("organization_id = ?", orgID).
			Updates(map[string]any{
				"used_bytes": gorm.Expr("used_bytes + ?", file.SizeBytes),
				"file_count": gorm.Expr("file_count + 1"),
				"updated_at": now,
			}).Error; err != nil {
			return err
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      actorType,
				ActorID:        &actorID,
				OrganizationID: &orgID,
				Action:         "MEDIA_FILE_RESTORED",
				ResourceType:   "STORED_FILE",
				ResourceID:     &file.ID,
				AfterData: map[string]any{
					"object_key": file.ObjectKey,
					"size_bytes": file.SizeBytes,
				},
				IPAddress: ip,
				UserAgent: ua,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	file.DeletedAt = gorm.DeletedAt{}
	file.TrashedAt = nil
	file.AccessURL = s.resolveAccessURL(ctx, &file)
	return &file, nil
}

// PurgeFileForActor permanently removes a trashed file from R2/S3 immediately and marks purged_at.
func (s *Service) PurgeFileForActor(ctx context.Context, orgID, fileID, actorID uuid.UUID, actorType, ip, ua string) error {
	if s.store == nil || !s.store.IsConfigured() {
		return ErrStorageNotConfigured
	}
	actorType = strings.ToUpper(strings.TrimSpace(actorType))
	if actorType == "" {
		actorType = "PLATFORM"
	}

	var file StoredFile
	if err := s.db.WithContext(ctx).Unscoped().
		First(&file, "id = ? AND organization_id = ? AND deleted_at IS NOT NULL AND purged_at IS NULL", fileID, orgID).Error; err != nil {
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
		if err := tx.Unscoped().Model(&StoredFile{}).
			Where("id = ? AND organization_id = ?", file.ID, orgID).
			Update("purged_at", now).Error; err != nil {
			return err
		}
		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:      actorType,
				ActorID:        &actorID,
				OrganizationID: &orgID,
				Action:         "MEDIA_FILE_PURGED",
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

// PurgeExpiredTrash physically deletes trashed files older than retentionDays from R2/S3 and marks purged_at.
func (s *Service) PurgeExpiredTrash(ctx context.Context, retentionDays, batchSize int) (int, error) {
	if s.db == nil || s.store == nil || !s.store.IsConfigured() {
		return 0, nil
	}
	if retentionDays <= 0 {
		retentionDays = TrashRetentionDays()
	}
	if batchSize <= 0 {
		batchSize = defaultPurgeBatchSize
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	var expired []StoredFile
	if err := s.db.WithContext(ctx).Unscoped().
		Where("deleted_at IS NOT NULL AND deleted_at <= ? AND purged_at IS NULL", cutoff).
		Order("deleted_at ASC").
		Limit(batchSize).
		Find(&expired).Error; err != nil {
		return 0, err
	}

	purged := 0
	for _, file := range expired {
		if err := s.store.DeleteObject(ctx, file.ObjectKey); err != nil {
			log.Printf("⚠️ Lỗi dọn file %s (%s) trên R2/S3: %v\n", file.ID, file.ObjectKey, err)
			continue
		}
		now := time.Now().UTC()
		if err := s.db.WithContext(ctx).Unscoped().Model(&StoredFile{}).
			Where("id = ?", file.ID).
			Update("purged_at", now).Error; err != nil {
			log.Printf("⚠️ Đã xóa object %s trên R2 nhưng lỗi cập nhật purged_at: %v\n", file.ObjectKey, err)
			continue
		}
		purged++
	}
	return purged, nil
}

// StartTrashCleanupWorker launches a background goroutine that periodically purges expired trashed files from R2/S3.
func (s *Service) StartTrashCleanupWorker(ctx context.Context) {
	if s.db == nil || s.store == nil || !s.store.IsConfigured() {
		return
	}
	retention := TrashRetentionDays()
	interval := purgeInterval()
	log.Printf("🧹 Đã bật Media Trash Cleanup Worker (giữ thùng rác %d ngày, quét mỗi %s)\n", retention, interval)

	go func() {
		runOnce := func() {
			runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			for {
				n, err := s.PurgeExpiredTrash(runCtx, retention, defaultPurgeBatchSize)
				if err != nil {
					log.Printf("⚠️ Media Trash Cleanup Worker lỗi: %v\n", err)
					return
				}
				if n > 0 {
					log.Printf("🧹 Media Trash Cleanup Worker đã xóa vĩnh viễn %d tệp quá hạn khỏi R2/S3\n", n)
				}
				if n < defaultPurgeBatchSize {
					return
				}
			}
		}

		// Chạy 1 lượt ngắn sau khi khởi động 15s, sau đó chạy định kỳ theo interval
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			runOnce()
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runOnce()
			}
		}
	}()
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
