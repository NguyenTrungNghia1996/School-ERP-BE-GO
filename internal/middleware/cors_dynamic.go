package middleware

import (
	"os"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

func buildSubdomainRegex(domain string) *regexp.Regexp {
	return regexp.MustCompile(`^https://[a-z0-9]([a-z0-9-]{1,38}[a-z0-9])?\.` + regexp.QuoteMeta(domain) + `$`)
}

func DynamicCORS() fiber.Handler {
	appEnv := strings.ToLower(os.Getenv("APP_ENV"))
	isDev := appEnv == "" || appEnv == "development" || appEnv == "local"

	domains := []string{"school-erp.vn"}
	if rootDomain := strings.ToLower(strings.TrimSpace(os.Getenv("CLOUDFLARE_ROOT_DOMAIN"))); rootDomain != "" && rootDomain != "school-erp.vn" {
		domains = append(domains, rootDomain)
	}

	var regexes []*regexp.Regexp
	exactOrigins := make(map[string]bool)
	for _, d := range domains {
		exactOrigins["https://"+d] = true
		exactOrigins["https://app."+d] = true
		exactOrigins["https://admin."+d] = true
		regexes = append(regexes, buildSubdomainRegex(d))
	}

	if extra := os.Getenv("CORS_ALLOWED_ORIGINS"); extra != "" {
		for _, o := range strings.Split(extra, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				exactOrigins[trimmed] = true
			}
		}
	}

	return cors.New(cors.Config{
		AllowOriginsFunc: func(origin string) bool {
			if exactOrigins[origin] {
				return true
			}
			for _, re := range regexes {
				if re.MatchString(origin) {
					return true
				}
			}
			if isDev && (strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1")) {
				return true
			}
			return false
		},
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:     "Origin, Content-Type, Accept, Accept-Language, Authorization, X-Tenant-Subdomain, X-School-ID, X-Request-ID, X-Requested-With",
		AllowCredentials: true,
	})
}
