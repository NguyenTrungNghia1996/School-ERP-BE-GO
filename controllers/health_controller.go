package controllers

import (
	"school-erp/config"
	"school-erp/utils"

	"github.com/gofiber/fiber/v2"
)

// HealthCheck godoc
// @Summary Check API and database health
// @Tags Health
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /health [get]
func HealthCheck(c *fiber.Ctx) error {
	sqlDB, err := config.DB.DB()
	if err != nil || sqlDB.Ping() != nil {
		return utils.JSONResponse(c, fiber.StatusServiceUnavailable, "Database disconnected", fiber.Map{
			"database": "disconnected",
		})
	}
	return utils.JSONResponse(c, fiber.StatusOK, "OK", fiber.Map{
		"database": "connected",
	})
}
