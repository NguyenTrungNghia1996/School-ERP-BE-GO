package platform

import (
	"school-erp/config"
	"school-erp/internal/middleware"
	"school-erp/internal/platform/audit"
	"school-erp/internal/platform/auth"
	"school-erp/internal/platform/billing"
	"school-erp/internal/platform/catalog"
	"school-erp/internal/platform/iam"
	"school-erp/internal/platform/media"
	"school-erp/internal/platform/menu"
	"school-erp/internal/platform/support"
	"school-erp/internal/platform/tenant_mgmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
)

func RegisterRoutes(api fiber.Router) {
	db := config.DB
	rdb := config.RedisClient
	cf := config.CloudflareClient

	auditService := audit.NewService(db)
	auditHandler := audit.NewHandler(auditService)

	menuService := menu.NewService(db, rdb, auditService)
	menuHandler := menu.NewHandler(menuService)

	authService := auth.NewService(db, rdb, menuService, auditService)
	authHandler := auth.NewHandler(authService)

	iamService := iam.NewService(db, rdb, auditService)
	iamHandler := iam.NewHandler(iamService)

	tenantService := tenant_mgmt.NewService(db, rdb, cf, auditService)
	tenantHandler := tenant_mgmt.NewHandler(tenantService)

	catalogService := catalog.NewService(db, auditService)
	catalogHandler := catalog.NewHandler(catalogService)

	billingService := billing.NewService(db, rdb, auditService)
	billingHandler := billing.NewHandler(billingService)

	mediaService := media.NewService(db, config.StorageClient, billingService, auditService)
	mediaHandler := media.NewHandler(mediaService)

	supportService := support.NewService(db, rdb, auditService)
	supportHandler := support.NewHandler(supportService)

	v1 := api.Group("/v1")

	// Public API: Phân giải Subdomain của Tổ chức & Redirect 301/308 (Rate-limited + Cache Redis 30m)
	v1.Get("/public/tenant-resolve", limiter.New(limiter.Config{
		Max:        120,
		Expiration: 1 * time.Minute,
	}), tenantHandler.PublicTenantResolve)

	p := v1.Group("/platform")

	// Nhóm 1: Auth & Profile (Public + Authenticated)
	p.Post("/auth/login", authHandler.Login)
	p.Post("/auth/refresh", authHandler.Refresh)

	protected := p.Group("", middleware.PlatformAuthMiddleware())
	protected.Post("/auth/logout", authHandler.Logout)
	protected.Get("/me", authHandler.Me)
	protected.Patch("/me", authHandler.UpdateMe)
	protected.Post("/me/change-password", authHandler.ChangePassword)
	protected.Patch("/me/password", authHandler.ChangePassword)
	protected.Post("/me/mfa/setup", authHandler.SetupMFA)
	protected.Post("/me/mfa/enable", authHandler.EnableMFA)
	protected.Post("/me/mfa/disable", authHandler.DisableMFA)

	// Nhóm 2: Quản trị Nhân sự & Phân quyền Platform (Legacy aliases + SPEC-BE-PLATFORM-IAM /iam/*)
	protected.Get("/users", middleware.RequirePlatformPermission("platform:user:read"), iamHandler.ListUsers)
	protected.Post("/users", middleware.RequirePlatformPermission("platform:user:manage"), iamHandler.CreateUser)
	protected.Put("/users/:id/roles", middleware.RequirePlatformPermission("platform:user:manage"), iamHandler.UpdateUserRoles)
	protected.Patch("/users/:id/status", middleware.RequirePlatformPermission("platform:user:manage"), iamHandler.UpdateUserStatus)
	protected.Patch("/users/:id/password", middleware.RequirePlatformPermission("platform:user:manage"), iamHandler.ResetUserPassword)
	protected.Delete("/users/:id/mfa", middleware.RequirePlatformPermission("platform:user:manage"), iamHandler.ForceDisableUserMFA)
	protected.Patch("/users/:id/mfa/disable", middleware.RequirePlatformPermission("platform:user:manage"), iamHandler.ForceDisableUserMFA)

	protected.Get("/roles", middleware.RequirePlatformPermission("platform:user:read"), iamHandler.ListRoles)
	protected.Post("/roles", middleware.RequirePlatformPermission("platform:role:manage"), iamHandler.CreateRole)
	protected.Put("/roles/:id/permissions", middleware.RequirePlatformPermission("platform:role:manage"), iamHandler.UpdateRolePermissions)
	protected.Get("/permissions", middleware.RequirePlatformPermission("platform:user:read"), iamHandler.ListPermissions)
	protected.Get("/menus/tree", middleware.RequirePlatformPermission("platform:user:read"), menuHandler.GetTree)

	// Namespace chuẩn SPEC-BE-PLATFORM-IAM v1.0 (/api/v1/platform/iam/*)
	iamGroup := protected.Group("/iam")
	iamGroup.Get("/users", middleware.RequirePlatformPermission("platform:user:read"), iamHandler.ListUsers)
	iamGroup.Get("/users/:id", middleware.RequirePlatformPermission("platform:user:read"), iamHandler.GetUser)
	iamGroup.Post("/users", middleware.RequirePlatformPermission("platform:user:create"), iamHandler.CreateUser)
	iamGroup.Patch("/users/:id", middleware.RequirePlatformPermission("platform:user:update"), iamHandler.UpdateUser)
	iamGroup.Put("/users/:id/roles", middleware.RequirePlatformPermission("platform:user:assign_role"), iamHandler.UpdateUserRoles)
	iamGroup.Post("/users/:id/reset-password", middleware.RequirePlatformPermission("platform:user:update"), iamHandler.ResetUserPassword)

	iamGroup.Get("/permissions", middleware.RequirePlatformPermission("platform:role:read"), iamHandler.ListPermissionsGrouped)
	iamGroup.Get("/roles", middleware.RequirePlatformPermission("platform:role:read"), iamHandler.ListRoles)
	iamGroup.Get("/roles/:id", middleware.RequirePlatformPermission("platform:role:read"), iamHandler.GetRole)
	iamGroup.Post("/roles", middleware.RequirePlatformPermission("platform:role:create"), iamHandler.CreateRole)
	iamGroup.Put("/roles/:id", middleware.RequirePlatformPermission("platform:role:update"), iamHandler.UpdateRole)
	iamGroup.Delete("/roles/:id", middleware.RequirePlatformPermission("platform:role:delete"), iamHandler.DeleteRole)

	// Nhóm 3: Quản trị Organization, Subdomain & Cloudflare DNS
	protected.Get("/organizations/check-subdomain", middleware.RequirePlatformPermission("platform:org:read"), tenantHandler.CheckSubdomain)
	protected.Get("/organizations", middleware.RequirePlatformPermission("platform:org:read"), tenantHandler.ListOrganizations)
	protected.Get("/organizations/:id", middleware.RequirePlatformPermission("platform:org:read"), tenantHandler.GetOrganization)
	protected.Post("/organizations", middleware.RequirePlatformPermission("platform:org:create"), tenantHandler.CreateOrganization)
	protected.Patch("/organizations/:id", middleware.RequirePlatformPermission("platform:org:create"), tenantHandler.UpdateOrganization)
	protected.Post("/organizations/:id/approve", middleware.RequirePlatformPermission("platform:org:approve"), tenantHandler.ApproveOrganization)
	protected.Patch("/organizations/:id/status", middleware.RequirePlatformPermission("platform:org:status"), tenantHandler.UpdateOrganizationStatus)
	protected.Post("/organizations/:id/change-subdomain", middleware.RequirePlatformPermission("platform:subdomain:manage"), tenantHandler.ChangeSubdomain)
	protected.Post("/organizations/:id/dns/retry", middleware.RequirePlatformPermission("platform:subdomain:manage"), tenantHandler.RetryDNS)

	protected.Delete("/subdomain-histories/:history_id/dns", middleware.RequirePlatformPermission("platform:subdomain:manage"), tenantHandler.DeleteRedirectHistoryDNS)
	protected.Get("/reserved-subdomains", middleware.RequirePlatformPermission("platform:subdomain:manage"), tenantHandler.ListReservedSubdomains)
	protected.Post("/reserved-subdomains", middleware.RequirePlatformPermission("platform:subdomain:manage"), tenantHandler.AddReservedSubdomain)
	protected.Post("/reserved-subdomains/sync-cloudflare", middleware.RequirePlatformPermission("platform:subdomain:manage"), tenantHandler.SyncReservedSubdomains)

	// Nhóm 4.1: Quản trị Catalog (Features, Permissions, Base Menus, Routes)
	protected.Get("/catalog/features", middleware.RequirePlatformPermission("platform:feature:manage"), catalogHandler.ListFeatures)
	protected.Post("/catalog/features", middleware.RequirePlatformPermission("platform:feature:manage"), catalogHandler.CreateFeature)
	protected.Put("/catalog/features/:id", middleware.RequirePlatformPermission("platform:feature:manage"), catalogHandler.UpdateFeature)

	protected.Get("/catalog/permissions", middleware.RequirePlatformPermission("platform:permission:manage"), catalogHandler.ListPermissions)
	protected.Post("/catalog/permissions", middleware.RequirePlatformPermission("platform:permission:manage"), catalogHandler.CreatePermission)
	protected.Put("/catalog/permissions/:id", middleware.RequirePlatformPermission("platform:permission:manage"), catalogHandler.UpdatePermission)

	protected.Get("/catalog/menus/tree", middleware.RequirePlatformPermission("platform:menu:manage"), catalogHandler.GetMenuTree)
	protected.Post("/catalog/menus", middleware.RequirePlatformPermission("platform:menu:manage"), catalogHandler.CreateMenu)
	protected.Put("/catalog/menus/reorder", middleware.RequirePlatformPermission("platform:menu:manage"), catalogHandler.ReorderMenus)
	protected.Get("/catalog/routes", middleware.RequirePlatformPermission("platform:menu:manage"), catalogHandler.ListRoutes)

	// Nhóm 4.2: Quản trị Gói dịch vụ & Phiên bản (Packages & Versions)
	protected.Get("/packages", middleware.RequirePlatformPermission("platform:package:read"), billingHandler.ListPackages)
	protected.Post("/packages", middleware.RequirePlatformPermission("platform:package:manage"), billingHandler.CreatePackage)
	protected.Get("/packages/:id", middleware.RequirePlatformPermission("platform:package:read"), billingHandler.GetPackage)
	protected.Post("/packages/:id/versions", middleware.RequirePlatformPermission("platform:package:manage"), billingHandler.CreatePackageVersion)

	protected.Put("/package-versions/:version_id", middleware.RequirePlatformPermission("platform:package:manage"), billingHandler.UpdatePackageVersion)
	protected.Post("/package-versions/:version_id/publish", middleware.RequirePlatformPermission("platform:package:manage"), billingHandler.PublishPackageVersion)
	protected.Post("/package-versions/:version_id/deprecate", middleware.RequirePlatformPermission("platform:package:manage"), billingHandler.DeprecatePackageVersion)

	// Nhóm 4.3: Cấp & Quản lý Subscription / Entitlement cho Organization
	protected.Get("/organizations/:org_id/subscriptions", middleware.RequirePlatformPermission("platform:org:read"), billingHandler.ListOrgSubscriptions)
	protected.Get("/organizations/:org_id/entitlements", middleware.RequirePlatformPermission("platform:org:read"), billingHandler.GetOrgEntitlements)
	protected.Post("/organizations/:org_id/subscriptions", middleware.RequirePlatformPermission("platform:subscription:manage"), billingHandler.CreateSubscription)
	protected.Post("/organizations/:org_id/switch-primary", middleware.RequirePlatformPermission("platform:subscription:manage"), billingHandler.SwitchPrimaryPackage)

	protected.Post("/subscriptions/:sub_id/renew", middleware.RequirePlatformPermission("platform:subscription:manage"), billingHandler.RenewSubscription)
	protected.Patch("/subscriptions/:sub_id/suspend", middleware.RequirePlatformPermission("platform:subscription:manage"), billingHandler.SuspendSubscription)
	protected.Patch("/subscriptions/:sub_id/revoke", middleware.RequirePlatformPermission("platform:subscription:manage"), billingHandler.RevokeSubscription)

	// Nhóm 5: Support Mode
	protected.Get("/support-sessions", middleware.RequirePlatformPermission("platform:support:request"), supportHandler.ListSessions)
	protected.Post("/support-sessions", middleware.RequirePlatformPermission("platform:support:request"), supportHandler.CreateSession)
	protected.Post("/support-sessions/:id/approve", middleware.RequirePlatformPermission("platform:support:approve"), supportHandler.ApproveSession)
	protected.Post("/support-sessions/:id/exchange-token", middleware.RequirePlatformPermission("platform:support:request"), supportHandler.ExchangeToken)
	protected.Post("/support-sessions/:id/revoke", middleware.RequirePlatformPermission("platform:support:revoke"), supportHandler.RevokeSession)

	// Nhóm 6: Audit Logs
	protected.Get("/audit-logs", middleware.RequirePlatformPermission("platform:audit:read"), auditHandler.List)

	// Nhóm 7: Lưu trữ ảnh/tài liệu S3 / Cloudflare R2 (cấp Platform & theo từng đơn vị)
	protected.Get("/media", middleware.RequirePlatformPermission("platform:media:read"), mediaHandler.ListPlatformFiles)
	protected.Post("/media/upload", middleware.RequirePlatformPermission("platform:media:manage"), mediaHandler.UploadPlatformFile)
	protected.Delete("/media/by-url", middleware.RequirePlatformPermission("platform:media:manage"), mediaHandler.DeletePlatformFileByURL)
	protected.Delete("/media/:file_id", middleware.RequirePlatformPermission("platform:media:manage"), mediaHandler.DeletePlatformFile)

	protected.Get("/organizations/:org_id/storage/usage", middleware.RequirePlatformPermission("platform:media:read"), mediaHandler.GetUsage)
	protected.Get("/organizations/:org_id/media", middleware.RequirePlatformPermission("platform:media:read"), mediaHandler.ListFiles)
	protected.Get("/organizations/:org_id/media/trash", middleware.RequirePlatformPermission("platform:media:read"), mediaHandler.ListTrash)
	protected.Get("/organizations/:org_id/media/:file_id/url", middleware.RequirePlatformPermission("platform:media:read"), mediaHandler.GetFileURL)
	protected.Post("/organizations/:org_id/media/upload", middleware.RequirePlatformPermission("platform:media:manage"), mediaHandler.UploadImage)
	protected.Post("/organizations/:org_id/media/:file_id/restore", middleware.RequirePlatformPermission("platform:media:manage"), mediaHandler.RestoreFile)
	protected.Delete("/organizations/:org_id/media/by-url", middleware.RequirePlatformPermission("platform:media:manage"), mediaHandler.DeleteFileByURL)
	protected.Delete("/organizations/:org_id/media/:file_id/permanent", middleware.RequirePlatformPermission("platform:media:manage"), mediaHandler.PurgeFile)
	protected.Delete("/organizations/:org_id/media/:file_id", middleware.RequirePlatformPermission("platform:media:manage"), mediaHandler.DeleteFile)
}
