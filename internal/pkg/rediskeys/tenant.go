package rediskeys

import (
	"fmt"
	"time"
)

const (
	TTLTenantAccess   = 15 * time.Minute
	TTLTenantSession  = 7 * 24 * time.Hour
	TTLTenantAuthz    = 15 * time.Minute
	TTLTenantUserOrgs = 30 * time.Minute
)

func TenantSessionKey(jti string) string {
	return fmt.Sprintf("tenant:session:%s", jti)
}

func TenantUserSessionsKey(userID string) string {
	return fmt.Sprintf("tenant:user:sessions:%s", userID)
}

func TenantBlacklistJTIKey(jti string) string {
	return fmt.Sprintf("tenant:blacklist:jti:%s", jti)
}

func TenantUserOrgsKey(userID string) string {
	return fmt.Sprintf("tenant:user:orgs:%s", userID)
}

func TenantMemberAuthzKey(membershipID string) string {
	return fmt.Sprintf("tenant:authz:member:%s", membershipID)
}
