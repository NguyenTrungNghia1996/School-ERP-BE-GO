package auth

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken      = errors.New("invalid token")
	ErrInvalidTokenRealm = errors.New("invalid token realm")
)

type TenantJWTClaims struct {
	UserID      string `json:"sub"`
	Realm       string `json:"realm"` // "tenant" hoặc "support"
	JTI         string `json:"jti"`
	TokenType   string `json:"token_type"` // "access" hoặc "refresh"
	OrgID       string `json:"org_id,omitempty"`
	SchoolID    string `json:"school_id,omitempty"`
	SessionType string `json:"session_type,omitempty"`
	jwt.RegisteredClaims
}

func getTenantSecret() []byte {
	secret := os.Getenv("TENANT_JWT_SECRET")
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}
	if secret == "" {
		secret = "school-erp-tenant-secret-key-2026"
	}
	return []byte(secret)
}

func getPlatformSecretForSupport() []byte {
	secret := os.Getenv("PLATFORM_JWT_SECRET")
	if secret == "" {
		secret = "school-erp-platform-secret-key-2026"
	}
	return []byte(secret)
}

func GenerateTenantToken(userID, orgID, schoolID, jti, tokenType string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := TenantJWTClaims{
		UserID:    userID,
		Realm:     "tenant",
		JTI:       jti,
		TokenType: tokenType,
		OrgID:     orgID,
		SchoolID:  schoolID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(getTenantSecret())
}

// ParseTenantOrSupportToken parses either a tenant JWT (realm="tenant") or a delegated Support Mode JWT (realm="support").
func ParseTenantOrSupportToken(tokenStr string) (*TenantJWTClaims, error) {
	// 1. Try tenant secret first
	claims, err := parseWithSecret(tokenStr, getTenantSecret())
	if err == nil && claims.Realm == "tenant" {
		return claims, nil
	}

	// 2. Try platform secret for Support Mode token (realm="support")
	supportClaims, err2 := parseWithSecret(tokenStr, getPlatformSecretForSupport())
	if err2 == nil {
		if supportClaims.Realm == "support" {
			return supportClaims, nil
		}
		return nil, ErrInvalidTokenRealm
	}

	if err == nil && claims.Realm != "tenant" {
		return nil, ErrInvalidTokenRealm
	}
	return nil, ErrInvalidToken
}

func ParseTenantRefreshToken(tokenStr string) (*TenantJWTClaims, error) {
	claims, err := parseWithSecret(tokenStr, getTenantSecret())
	if err != nil {
		return nil, ErrInvalidToken
	}
	if claims.Realm != "tenant" {
		return nil, ErrInvalidTokenRealm
	}
	if claims.TokenType != "refresh" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func parseWithSecret(tokenStr string, secret []byte) (*TenantJWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &TenantJWTClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*TenantJWTClaims)
	if !ok || !token.Valid || claims.UserID == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
