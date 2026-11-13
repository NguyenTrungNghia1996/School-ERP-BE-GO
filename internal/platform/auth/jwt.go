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

type PlatformJWTClaims struct {
	UserID    string `json:"sub"`
	Realm     string `json:"realm"`      // Bắt buộc phải là "platform" (hoặc "support" cho Support Mode)
	JTI       string `json:"jti"`        // Session ID lưu trên Redis
	TokenType string `json:"token_type"` // "access" hoặc "refresh"
	OrgID     string `json:"org_id,omitempty"`
	SchoolID  string `json:"school_id,omitempty"`
	SessionType string `json:"session_type,omitempty"`
	jwt.RegisteredClaims
}

func getPlatformSecret() []byte {
	secret := os.Getenv("PLATFORM_JWT_SECRET")
	if secret == "" {
		secret = "school-erp-platform-secret-key-2026"
	}
	return []byte(secret)
}

func GeneratePlatformToken(userID, jti, tokenType string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := PlatformJWTClaims{
		UserID:    userID,
		Realm:     "platform",
		JTI:       jti,
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(getPlatformSecret())
}

func GenerateSupportToken(actorUserID, sessionID, orgID, schoolID, sessionType string, expiresAt time.Time) (string, error) {
	now := time.Now()
	claims := PlatformJWTClaims{
		UserID:      actorUserID,
		Realm:       "support",
		JTI:         sessionID,
		TokenType:   "access",
		OrgID:       orgID,
		SchoolID:    schoolID,
		SessionType: sessionType,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   actorUserID,
			ID:        sessionID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(getPlatformSecret())
}

func ParsePlatformToken(tokenStr string) (*PlatformJWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &PlatformJWTClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return getPlatformSecret(), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*PlatformJWTClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	if claims.Realm != "platform" {
		return nil, ErrInvalidTokenRealm
	}

	return claims, nil
}
