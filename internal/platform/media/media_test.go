package media_test

import (
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
