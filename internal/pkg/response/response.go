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
	ErrCodeSubdomainReservedOrTaken  = "SUBDOMAIN_RESERVED_OR_TAKEN"
	ErrCodePackageVersionImmutable   = "PACKAGE_VERSION_IMMUTABLE"
	ErrCodePrimarySubscriptionOverlap = "PRIMARY_SUBSCRIPTION_OVERLAP"
	ErrCodePackageVersionNotPublished = "PACKAGE_VERSION_NOT_PUBLISHED"
	ErrCodeFeatureInUseCannotModify  = "FEATURE_IN_USE_CANNOT_MODIFY"
	ErrCodeLastRootAdminProtected     = "LAST_ROOT_ADMIN_PROTECTED"
	ErrCodeSelfLockNotAllowed         = "SELF_LOCK_NOT_ALLOWED"
	ErrCodeSelfModificationNotAllowed = "SELF_MODIFICATION_NOT_ALLOWED"
	ErrCodeSystemRoleProtected        = "SYSTEM_ROLE_PROTECTED"
	ErrCodePlatformEmailAlreadyExists = "PLATFORM_EMAIL_ALREADY_EXISTS"
	ErrCodeRoleInUse                  = "ROLE_IN_USE"
	ErrCodeValidationFailed           = "VALIDATION_FAILED"
	ErrCodeNotFound                 = "RESOURCE_NOT_FOUND"
	ErrCodeConflict                 = "RESOURCE_CONFLICT"
	ErrCodeInternalError            = "INTERNAL_SERVER_ERROR"
)

type Meta struct {
	Page  int   `json:"page" example:"1"`
	Limit int   `json:"limit" example:"20"`
	Total int64 `json:"total" example:"100"`
}

type ErrorBody struct {
	Code    string `json:"code" example:"VALIDATION_FAILED"`
	Message string `json:"message" example:"Dữ liệu đầu vào không hợp lệ"`
	Details any    `json:"details"`
}

type SuccessResponse struct {
	Success bool   `json:"success" example:"true"`
	Message string `json:"message" example:"Thành công"`
	Data    any    `json:"data"`
}

type SuccessListResponse struct {
	Success bool   `json:"success" example:"true"`
	Message string `json:"message" example:"Thành công"`
	Data    any    `json:"data"`
	Meta    Meta   `json:"meta"`
}

type ErrorResponse struct {
	Success bool      `json:"success" example:"false"`
	Message string    `json:"message" example:"Dữ liệu đầu vào không hợp lệ"`
	Error   ErrorBody `json:"error"`
}

func Success(c *fiber.Ctx, status int, data any) error {
	return c.Status(status).JSON(fiber.Map{
		"success": true,
		"message": "Thành công",
		"data":    data,
	})
}

func SuccessWithMeta(c *fiber.Ctx, status int, data any, meta Meta) error {
	return c.Status(status).JSON(fiber.Map{
		"success": true,
		"message": "Thành công",
		"data":    data,
		"meta":    meta,
	})
}

func Error(c *fiber.Ctx, status int, code string, message string, details any) error {
	return c.Status(status).JSON(fiber.Map{
		"success": false,
		"message": message,
		"error": ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}
