package response

import "github.com/gofiber/fiber/v2"

// Error codes theo đặc tả SPEC-BE-PLATFORM-ADMIN mục 6.2
const (
	ErrCodeInvalidTokenRealm        = "INVALID_TOKEN_REALM"
	ErrCodeSessionExpiredOrRevoked  = "SESSION_EXPIRED_OR_REVOKED"
	ErrCodeTokenRevoked             = "TOKEN_REVOKED"
	ErrCodeSessionExpired           = "SESSION_EXPIRED"
	ErrCodeUnauthorized             = "UNAUTHORIZED"
	ErrCodeInvalidCredentials       = "INVALID_CREDENTIALS"
	ErrCodeAccountLocked            = "ACCOUNT_LOCKED"
	ErrCodeMFARequired              = "MFA_REQUIRED"
	ErrCodeMFAInvalid               = "MFA_INVALID"
	ErrCodePlatformPermissionDenied = "PLATFORM_PERMISSION_DENIED"
	ErrCodeSelfApprovalNotAllowed   = "SELF_APPROVAL_NOT_ALLOWED"
	ErrCodeSubdomainReservedOrTaken = "SUBDOMAIN_RESERVED_OR_TAKEN"
	ErrCodePackageVersionImmutable  = "PACKAGE_VERSION_IMMUTABLE"
	ErrCodeLastRootAdminProtected   = "LAST_ROOT_ADMIN_PROTECTED"
	ErrCodeSelfLockNotAllowed       = "SELF_LOCK_NOT_ALLOWED"
	ErrCodeValidationFailed         = "VALIDATION_FAILED"
	ErrCodeNotFound                 = "RESOURCE_NOT_FOUND"
	ErrCodeConflict                 = "RESOURCE_CONFLICT"
	ErrCodeInternalError            = "INTERNAL_SERVER_ERROR"
)

type Meta struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

func Success(c *fiber.Ctx, status int, data any) error {
	return c.Status(status).JSON(fiber.Map{
		"success": true,
		"data":    data,
	})
}

func SuccessWithMeta(c *fiber.Ctx, status int, data any, meta Meta) error {
	return c.Status(status).JSON(fiber.Map{
		"success": true,
		"data":    data,
		"meta":    meta,
	})
}

func Error(c *fiber.Ctx, status int, code string, message string, details any) error {
	return c.Status(status).JSON(fiber.Map{
		"success": false,
		"error": ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}
