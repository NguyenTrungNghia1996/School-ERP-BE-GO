package routes

import (
	"school-erp/controllers"
	"school-erp/internal/platform"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
)

func Setup(app *fiber.App) {
	app.Get("/swagger/*", swagger.HandlerDefault)

	api := app.Group("/api")
	api.Get("/health", controllers.HealthCheck)

	platform.RegisterRoutes(api)
}
