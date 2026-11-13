package rediskeys

import (
	"fmt"
	"time"
)

const (
	TTLPlatformAccess      = 15 * time.Minute
	TTLPlatformSession     = 7 * 24 * time.Hour // Khớp Refresh Token
	TTLPlatformAuthz       = 15 * time.Minute
	TTLPlatformMenuTree    = 1 * time.Hour
	TTLTenantSubdomain     = 30 * time.Minute
	TTLTenantEntitlement   = 30 * time.Minute
	EmptyAuthzPlaceholder  = "__EMPTY__"
)

func PlatformSessionKey(jti string) string {
	return fmt.Sprintf("platform:session:%s", jti)
}

func PlatformUserSessionsKey(userID string) string {
	return fmt.Sprintf("platform:user:sessions:%s", userID)
}

func PlatformBlacklistJTIKey(jti string) string {
	return fmt.Sprintf("platform:blacklist:jti:%s", jti)
}

func PlatformUserAuthzKey(userID string) string {
	return fmt.Sprintf("platform:authz:user:%s", userID)
}

func PlatformMenuTreeKey() string {
	return "platform:menu:tree"
}

func TenantSubdomainKey(subdomain string) string {
	return fmt.Sprintf("tenant:subdomain:%s", subdomain)
}

func TenantEntitlementKey(orgID string) string {
	return fmt.Sprintf("tenant:entitlement:%s", orgID)
}

func SupportSessionKey(sessionID string) string {
	return fmt.Sprintf("support:session:%s", sessionID)
}
