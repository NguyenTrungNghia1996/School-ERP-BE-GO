package platform_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"school-erp/internal/middleware"
	"school-erp/internal/pkg/response"
	"school-erp/internal/platform/auth"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestCrossRealmTokenRejection(t *testing.T) {
	app := fiber.New()
	app.Get("/api/v1/platform/me", middleware.PlatformAuthMiddleware(), func(c *fiber.Ctx) error {
		return response.Success(c, fiber.StatusOK, fiber.Map{"ok": true})
	})

	// 1. Forge a token with realm = "tenant"
	claims := auth.PlatformJWTClaims{
		UserID:    uuid.New().String(),
		Realm:     "tenant",
		JTI:       uuid.New().String(),
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("school-erp-platform-secret-key-2026"))
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/me", nil)
	req.Header.Set("Authorization", "Bearer "+signed)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}

	var body struct {
		Success bool               `json:"success"`
		Error   response.ErrorBody `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.Error.Code != response.ErrCodeInvalidTokenRealm {
		t.Fatalf("expected error code %s, got %s", response.ErrCodeInvalidTokenRealm, body.Error.Code)
	}

	// 2. Verify Support Mode token (realm = "support") is also rejected on Platform Admin routes
	supportToken, err := auth.GenerateSupportToken(
		uuid.New().String(),
		uuid.New().String(),
		uuid.New().String(),
		uuid.New().String(),
		"READ_ONLY",
		time.Now().Add(30*time.Minute),
	)
	if err != nil {
		t.Fatalf("failed to generate support token: %v", err)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/platform/me", nil)
	req2.Header.Set("Authorization", "Bearer "+supportToken)
	resp2, err := app.Test(req2)
	if err != nil || resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for support token on platform endpoint, got %v", resp2.StatusCode)
	}
}

func TestMFASecretAndOTPAuthURL(t *testing.T) {
	secret, err := auth.GenerateMFASecret()
	if err != nil || len(secret) < 16 {
		t.Fatalf("expected valid MFA secret, got %q (err=%v)", secret, err)
	}

	uri := auth.BuildOTPAuthURL("SchoolERP-Platform", "admin@school-erp.vn", secret)
	if uri == "" {
		t.Fatal("expected non-empty otpauth URL")
	}
}

