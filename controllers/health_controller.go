package controllers

import (
	"context"
	"school-erp/config"
	"school-erp/utils"
	"time"

	"github.com/gofiber/fiber/v2"
)

// HealthCheck godoc
// @Summary Check API, database and Redis health
// @Tags Health
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /health [get]
func HealthCheck(c *fiber.Ctx) error {
	dbStatus := "connected"
	sqlDB, err := config.DB.DB()
	if err != nil || sqlDB.Ping() != nil {
		dbStatus = "disconnected"
	}

	redisStatus := "disconnected"
	if config.RedisClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := config.RedisClient.Ping(ctx).Err(); err == nil {
			redisStatus = "connected"
		}
	}

	if dbStatus == "disconnected" {
		return utils.JSONResponse(c, fiber.StatusServiceUnavailable, "Database disconnected", fiber.Map{
			"database": dbStatus,
			"redis":    redisStatus,
		})
	}

	return utils.JSONResponse(c, fiber.StatusOK, "OK", fiber.Map{
		"database": dbStatus,
		"redis":    redisStatus,
	})
}
