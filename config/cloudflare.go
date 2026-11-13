package config

import (
	"log"
	"os"
	"school-erp/internal/pkg/cloudflare"
	"strconv"
	"strings"
)

var CloudflareClient *cloudflare.Client

// InitCloudflare initializes the global Cloudflare DNS v4 client from environment variables.
func InitCloudflare() {
	apiToken := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN"))
	zoneID := strings.TrimSpace(os.Getenv("CLOUDFLARE_ZONE_ID"))
	rootDomain := strings.TrimSpace(os.Getenv("CLOUDFLARE_ROOT_DOMAIN"))
	if rootDomain == "" {
		rootDomain = "school-erp.vn"
	}
	targetHost := strings.TrimSpace(os.Getenv("CLOUDFLARE_TENANT_TARGET"))
	if targetHost == "" {
		targetHost = "tenant-ingress.school-erp.vn"
	}

	proxied := true
	if pStr := strings.ToLower(strings.TrimSpace(os.Getenv("CLOUDFLARE_PROXIED"))); pStr == "false" || pStr == "0" {
		proxied = false
	}

	ttl := 1
	if ttlStr := strings.TrimSpace(os.Getenv("CLOUDFLARE_TTL")); ttlStr != "" {
		if parsed, err := strconv.Atoi(ttlStr); err == nil && parsed > 0 {
			ttl = parsed
		}
	}

	CloudflareClient = cloudflare.NewClient(apiToken, zoneID, rootDomain, targetHost, proxied, ttl)

	if CloudflareClient.IsConfigured() {
		log.Printf("✅ Đã khởi tạo Cloudflare DNS Client (Zone: %s, Domain: *.%s -> %s)\n", zoneID, rootDomain, targetHost)
	} else {
		log.Printf("⚠️ Chưa cấu hình CLOUDFLARE_API_TOKEN / CLOUDFLARE_ZONE_ID (DNS Provisioning sẽ chờ cấu hình hoặc Retry)\n")
	}
}
