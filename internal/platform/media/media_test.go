package media_test

import (
	"archive/zip"
	"bytes"
	"school-erp/internal/platform/media"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTenantFolderIsolationAndImageValidation(t *testing.T) {
	org1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	org2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	schoolID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	fileID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

	k1 := media.BuildOrgObjectKey(org1, nil, "logos", fileID, ".png", now)
	if k1 != "orgs/11111111-1111-1111-1111-111111111111/logos/2026/11/44444444-4444-4444-4444-444444444444.png" {
		t.Fatalf("unexpected org1 key: %s", k1)
	}

	k2 := media.BuildOrgObjectKey(org2, &schoolID, "avatars", fileID, ".jpg", now)
	if !strings.HasPrefix(k2, "orgs/22222222-2222-2222-2222-222222222222/schools/33333333-3333-3333-3333-333333333333/avatars/") {
		t.Fatalf("unexpected org2 school key: %s", k2)
	}

	// Reject path traversal in folder name
	if _, err := media.SanitizeFolder("../secret"); err != media.ErrInvalidFolder {
		t.Fatalf("expected ErrInvalidFolder for traversal, got %v", err)
	}

	// Reject non-image payload even if filename claims .png
	if _, _, err := media.ValidateImagePayload([]byte("<script>alert(1)</script>")); err != media.ErrInvalidImageType {
		t.Fatalf("expected ErrInvalidImageType for HTML payload, got %v", err)
	}

	// Accept valid PNG magic header
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	mime, ext, err := media.ValidateImagePayload(pngHeader)
	if err != nil || mime != "image/png" || ext != ".png" {
		t.Fatalf("expected image/png (.png), got %s (%s), err=%v", mime, ext, err)
	}
}

func buildTestZip(t *testing.T, entries []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte("<xml/>")); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestDocumentValidation(t *testing.T) {
	// PDF
	mime, ext, err := media.ValidateFilePayload([]byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj"), "report.pdf")
	if err != nil || mime != "application/pdf" || ext != ".pdf" {
		t.Fatalf("PDF failed: mime=%s ext=%s err=%v", mime, ext, err)
	}

	// DOCX
	docxBytes := buildTestZip(t, []string{"[Content_Types].xml", "word/document.xml"})
	mime, ext, err = media.ValidateFilePayload(docxBytes, "lesson.docx")
	if err != nil || mime != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || ext != ".docx" {
		t.Fatalf("DOCX failed: mime=%s ext=%s err=%v", mime, ext, err)
	}

	// XLSX
	xlsxBytes := buildTestZip(t, []string{"[Content_Types].xml", "xl/workbook.xml"})
	mime, ext, err = media.ValidateFilePayload(xlsxBytes, "grades.xlsx")
	if err != nil || mime != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" || ext != ".xlsx" {
		t.Fatalf("XLSX failed: mime=%s ext=%s err=%v", mime, ext, err)
	}

	// Arbitrary ZIP disguised as .docx MUST be rejected
	fakeZip := buildTestZip(t, []string{"malware.exe"})
	if _, _, err := media.ValidateFilePayload(fakeZip, "fake.docx"); err != media.ErrInvalidFileType {
		t.Fatalf("expected ErrInvalidFileType for arbitrary zip, got %v", err)
	}

	// Legacy DOC & XLS (OLE2 header)
	ole2 := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1, 0x00, 0x00}
	mime, ext, err = media.ValidateFilePayload(ole2, "old.doc")
	if err != nil || mime != "application/msword" || ext != ".doc" {
		t.Fatalf("DOC failed: mime=%s ext=%s err=%v", mime, ext, err)
	}
	mime, ext, err = media.ValidateFilePayload(ole2, "old.xls")
	if err != nil || mime != "application/vnd.ms-excel" || ext != ".xls" {
		t.Fatalf("XLS failed: mime=%s ext=%s err=%v", mime, ext, err)
	}
	if _, _, err := media.ValidateFilePayload(ole2, "unknown.bin"); err != media.ErrInvalidFileType {
		t.Fatalf("expected ErrInvalidFileType for unknown OLE2 ext, got %v", err)
	}
}
