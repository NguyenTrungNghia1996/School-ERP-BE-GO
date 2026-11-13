package middleware

import (
	"os"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

var subdomainOriginRegex = regexp.MustCompile(`^https://[a-z0-9]([a-z0-9-]{1,38}[a-z0-9])?\.school-erp\.vn$`)

func DynamicCORS() fiber.Handler {
	appEnv := strings.ToLower(os.Getenv("APP_ENV"))
	isDev := appEnv == "" || appEnv == "development" || appEnv == "local"

	return cors.New(cors.Config{
		AllowOriginsFunc: func(origin string) bool {
			if origin == "https://admin.school-erp.vn" || origin == "https://app.school-erp.vn" || origin == "https://school-erp.vn" {
				return true
			}
			if subdomainOriginRegex.MatchString(origin) {
				return true
			}
			if isDev && (strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1")) {
				return true
			}
			return false
		},
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization, X-Tenant-Subdomain, X-Request-ID",
		AllowCredentials: true,
	})
}
