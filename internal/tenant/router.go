package tenant

import (
	"school-erp/config"
	"school-erp/internal/middleware"
	"school-erp/internal/platform/audit"
	"school-erp/internal/platform/billing"
	platformmedia "school-erp/internal/platform/media"
	tenantauth "school-erp/internal/tenant/auth"
	tenantiam "school-erp/internal/tenant/iam"
	tenantmedia "school-erp/internal/tenant/media"
	tenantmenu "school-erp/internal/tenant/menu"
	"school-erp/internal/tenant/org_school"

	"github.com/gofiber/fiber/v2"
)

func RegisterRoutes(api fiber.Router) {
	db := config.DB
	rdb := config.RedisClient

	auditSvc := audit.NewService(db)
	billingSvc := billing.NewService(db, rdb, auditSvc)

	menuSvc := tenantmenu.NewService(db, billingSvc, auditSvc)
	menuHandler := tenantmenu.NewHandler(menuSvc)

	authSvc := tenantauth.NewService(db, rdb, billingSvc, menuSvc)
	authHandler := tenantauth.NewHandler(authSvc)

	orgSchoolSvc := org_school.NewService(db, billingSvc, auditSvc)
	orgSchoolHandler := org_school.NewHandler(orgSchoolSvc)

	iamSvc := tenantiam.NewService(db, rdb, billingSvc, auditSvc)
	iamHandler := tenantiam.NewHandler(iamSvc)

	mediaSvc := platformmedia.NewService(db, config.StorageClient, billingSvc, auditSvc)
	mediaHandler := tenantmedia.NewHandler(mediaSvc)

	v1 := api.Group("/v1")

	// Nhóm 1: Auth công khai cho Tenant
	v1.Post("/auth/login", authHandler.Login)
	v1.Post("/auth/refresh", authHandler.Refresh)

	// Các route bên dưới đều qua TenantResolver (xác minh chéo JWT + X-Tenant-Subdomain + Membership/Support)
	t := v1.Group("", middleware.TenantResolver())

	// Nhóm 1 (Authenticated): Ngữ cảnh người dùng & chuyển trường
	t.Get("/me", authHandler.Me)
	t.Post("/auth/switch-school", authHandler.SwitchSchool)
	t.Post("/auth/logout", authHandler.Logout)

	// Nhóm 2: Thông tin Đơn vị & Quản lý Trường trực thuộc
	t.Get("/organization", orgSchoolHandler.GetOrganization)
	t.Patch("/organization", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:settings:update"), orgSchoolHandler.UpdateOrganization)
	t.Get("/schools", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "school:read"), orgSchoolHandler.ListSchools)
	t.Post("/schools", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "school:create"), orgSchoolHandler.CreateSchool)
	t.Put("/schools/:id", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "school:update"), orgSchoolHandler.UpdateSchool)

	// Nhóm 3: Nhân sự & Phân quyền tại Đơn vị
	t.Get("/iam/members", middleware.RequireTenantAccess(billingSvc, "CORE_IAM", "iam:member:read"), iamHandler.ListMembers)
	t.Post("/iam/members", middleware.RequireTenantAccess(billingSvc, "CORE_IAM", "iam:member:create"), iamHandler.CreateMember)
	t.Put("/iam/members/:membership_id/roles", middleware.RequireTenantAccess(billingSvc, "CORE_IAM", "iam:member:assign_role"), iamHandler.UpdateMemberRoles)
	t.Patch("/iam/members/:membership_id/status", middleware.RequireTenantAccess(billingSvc, "CORE_IAM", "iam:member:assign_role"), iamHandler.UpdateMemberStatus)
	t.Get("/iam/entitled-permissions", middleware.RequireTenantAccess(billingSvc, "CORE_IAM", "iam:role:read"), iamHandler.ListEntitledPermissions)
	t.Get("/iam/roles", middleware.RequireTenantAccess(billingSvc, "CORE_IAM", "iam:role:manage"), iamHandler.ListRoles)
	t.Post("/iam/roles", middleware.RequireTenantAccess(billingSvc, "CORE_IAM", "iam:role:manage"), iamHandler.CreateRole)
	t.Put("/iam/roles/:id", middleware.RequireTenantAccess(billingSvc, "CORE_IAM", "iam:role:manage"), iamHandler.UpdateRole)
	t.Delete("/iam/roles/:id", middleware.RequireTenantAccess(billingSvc, "CORE_IAM", "iam:role:manage"), iamHandler.DeleteRole)

	// Nhóm 4: Tùy chỉnh Menu Đơn vị
	t.Get("/menus/manage", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:menu:manage"), menuHandler.GetManageTree)
	t.Get("/menus/routes", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:menu:manage"), menuHandler.ListEntitledRoutes)
	t.Put("/menus/customize", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:menu:manage"), menuHandler.CustomizeMenus)
	t.Post("/menus/custom-items", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:menu:manage"), menuHandler.CreateCustomItem)
	t.Delete("/menus/custom-items/:id", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:menu:manage"), menuHandler.DeleteCustomItem)

	// Nhóm 5: Lưu trữ ảnh/tài liệu S3 / Cloudflare R2 & Dung lượng của Đơn vị / Trường học
	t.Get("/storage/usage", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:media:read"), mediaHandler.GetUsage)
	t.Get("/media", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:media:read"), mediaHandler.ListFiles)
	t.Get("/media/trash", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:media:read"), mediaHandler.ListTrash)
	t.Get("/media/:file_id/url", mediaHandler.GetFileURL)
	t.Post("/media/upload", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:media:manage"), mediaHandler.UploadImage)
	t.Post("/media/:file_id/restore", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:media:manage"), mediaHandler.RestoreFile)
	t.Delete("/media/by-url", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:media:manage"), mediaHandler.DeleteFileByURL)
	t.Delete("/media/:file_id/permanent", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:media:manage"), mediaHandler.PurgeFile)
	t.Delete("/media/:file_id", middleware.RequireTenantAccess(billingSvc, "CORE_ORG", "org:media:manage"), mediaHandler.DeleteFile)
}
