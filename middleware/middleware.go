package middleware

import (
	internalmw "school-erp/internal/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func Setup(app *fiber.App) {
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(internalmw.DynamicCORS())
}
