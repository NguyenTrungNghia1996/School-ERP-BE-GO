package iam_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"school-erp/internal/pkg/response"
	"school-erp/internal/platform/iam"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func setupIAMTestApp(actorID string) *fiber.App {
	app := fiber.New()
	handler := iam.NewHandler(iam.NewService(nil, nil, nil))

	app.Use(func(c *fiber.Ctx) error {
		c.Locals("platform_user_id", actorID)
		return c.Next()
	})

	app.Patch("/api/v1/platform/iam/users/:id", handler.UpdateUser)
	app.Put("/api/v1/platform/iam/users/:id/roles", handler.UpdateUserRoles)
	app.Patch("/api/v1/platform/users/:id/status", handler.UpdateUserStatus)
	app.Post("/api/v1/platform/iam/roles", handler.CreateRole)
	return app
}

func assertErrorResponse(t *testing.T, resp *http.Response, expectedStatus int, expectedCode string) {
	t.Helper()
	if resp.StatusCode != expectedStatus {
		t.Fatalf("expected HTTP %d, got %d", expectedStatus, resp.StatusCode)
	}
	var body struct {
		Success bool               `json:"success"`
		Error   response.ErrorBody `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode error body: %v", err)
	}
	if body.Success {
		t.Fatalf("expected success=false")
	}
	if body.Error.Code != expectedCode {
		t.Fatalf("expected error code %s, got %s", expectedCode, body.Error.Code)
	}
}

func TestIAMSelfModificationGuards(t *testing.T) {
	actorID := uuid.New().String()
	app := setupIAMTestApp(actorID)

	// 1. Self role modification via PUT /iam/users/:id/roles -> 403 SELF_MODIFICATION_NOT_ALLOWED
	reqRoles := httptest.NewRequest(http.MethodPut, "/api/v1/platform/iam/users/"+actorID+"/roles",
		bytes.NewBufferString(`{"role_ids":["00000000-0000-0000-0000-000000000001"]}`))
	reqRoles.Header.Set("Content-Type", "application/json")
	respRoles, err := app.Test(reqRoles)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	assertErrorResponse(t, respRoles, http.StatusForbidden, response.ErrCodeSelfModificationNotAllowed)

	// 2. Self lock via PATCH /users/:id/status -> 403 SELF_MODIFICATION_NOT_ALLOWED
	reqStatus := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/users/"+actorID+"/status",
		bytes.NewBufferString(`{"status":"LOCKED","reason":"self lock test"}`))
	reqStatus.Header.Set("Content-Type", "application/json")
	respStatus, err := app.Test(reqStatus)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	assertErrorResponse(t, respStatus, http.StatusForbidden, response.ErrCodeSelfModificationNotAllowed)

	// 3. Invalid role code format on POST /iam/roles -> 400 VALIDATION_FAILED
	reqRole := httptest.NewRequest(http.MethodPost, "/api/v1/platform/iam/roles",
		bytes.NewBufferString(`{"code":"bad-role!","name":"Invalid Role"}`))
	reqRole.Header.Set("Content-Type", "application/json")
	respRole, err := app.Test(reqRole)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	assertErrorResponse(t, respRole, http.StatusBadRequest, response.ErrCodeValidationFailed)
}
