package routes

import (
	"school-erp/controllers"
	"school-erp/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
)

func Setup(app *fiber.App) {
	app.Get("/swagger/*", swagger.HandlerDefault)

	api := app.Group("/api")
	api.Get("/health", controllers.HealthCheck)

	v1 := api.Group("/v1")

	// Platform Super Admin namespace (admin.school-erp.vn) — no TenantResolver
	platform := v1.Group("/platform", middleware.RequirePlatformAdmin)
	platform.Get("/me", controllers.PlatformMe)

	// Organization Tenant namespace ({subdomain}.school-erp.vn) — enforces TenantResolver + membership/support check
	tenant := v1.Group("/tenant", middleware.TenantResolver)
	tenant.Get("/context", controllers.TenantContext)
}
