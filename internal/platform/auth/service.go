package auth

import (
	"context"
	"errors"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/audit"
	"school-erp/internal/platform/iam"
	"school-erp/internal/platform/menu"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrAccountLocked      = errors.New("account is locked or inactive")
	ErrMFARequired        = errors.New("mfa code is required")
	ErrMFAInvalid         = errors.New("invalid mfa code")
	ErrMFANotSetup        = errors.New("mfa secret has not been initialized")
	ErrMFAAlreadyEnabled  = errors.New("mfa is already enabled")
	ErrMFANotEnabled      = errors.New("mfa is not enabled")
	ErrSessionExpired     = errors.New("session expired or revoked")
)

type Service struct {
	db          *gorm.DB
	rdb         *redis.Client
	menuService *menu.Service
	auditor     *audit.Service
}

func NewService(db *gorm.DB, rdb *redis.Client, menuService *menu.Service, auditor *audit.Service) *Service {
	return &Service{
		db:          db,
		rdb:         rdb,
		menuService: menuService,
		auditor:     auditor,
	}
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	OTPCode  string `json:"otp_code"`
}

type LoginOutput struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func (s *Service) Login(ctx context.Context, input LoginInput) (*LoginOutput, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	var user iam.PlatformUser
	if err := s.db.WithContext(ctx).Where("LOWER(email) = ?", email).First(&user).Error; err != nil {
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	if user.Status != "ACTIVE" {
		return nil, ErrAccountLocked
	}

	if user.MFAEnabled {
		if strings.TrimSpace(input.OTPCode) == "" {
			return nil, ErrMFARequired
		}
		secret := ""
		if user.MFASecret != nil {
			secret = *user.MFASecret
		}
		if !VerifyTOTP(secret, input.OTPCode) {
			return nil, ErrMFAInvalid
		}
	}

	jti := uuid.New().String()
	userIDStr := user.ID.String()

	accessToken, err := GeneratePlatformToken(userIDStr, jti, "access", rediskeys.TTLPlatformAccess)
	if err != nil {
		return nil, err
	}

	refreshToken, err := GeneratePlatformToken(userIDStr, jti, "refresh", rediskeys.TTLPlatformSession)
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		pipe := s.rdb.Pipeline()
		pipe.Set(ctx, rediskeys.PlatformSessionKey(jti), userIDStr, rediskeys.TTLPlatformSession)
		pipe.SAdd(ctx, rediskeys.PlatformUserSessionsKey(userIDStr), jti)
		pipe.Expire(ctx, rediskeys.PlatformUserSessionsKey(userIDStr), rediskeys.TTLPlatformSession)
		_, _ = pipe.Exec(ctx)
	}

	now := time.Now()
	_ = s.db.WithContext(ctx).Model(&user).Update("last_login_at", now).Error

	return &LoginOutput{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(rediskeys.TTLPlatformAccess.Seconds()),
	}, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (*LoginOutput, error) {
	claims, err := ParsePlatformToken(strings.TrimSpace(refreshToken))
	if err != nil {
		return nil, err
	}

	if claims.TokenType != "" && claims.TokenType != "refresh" {
		return nil, ErrInvalidToken
	}

	if s.rdb != nil {
		blacklisted, err := s.rdb.Exists(ctx, rediskeys.PlatformBlacklistJTIKey(claims.JTI)).Result()
		if err == nil && blacklisted > 0 {
			return nil, ErrSessionExpired
		}

		exists, err := s.rdb.Exists(ctx, rediskeys.PlatformSessionKey(claims.JTI)).Result()
		if err == nil && exists == 0 {
			return nil, ErrSessionExpired
		}
	}

	var user iam.PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", claims.UserID).Error; err != nil {
		return nil, ErrSessionExpired
	}
	if user.Status != "ACTIVE" {
		return nil, ErrAccountLocked
	}

	accessToken, err := GeneratePlatformToken(claims.UserID, claims.JTI, "access", rediskeys.TTLPlatformAccess)
	if err != nil {
		return nil, err
	}

	return &LoginOutput{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(rediskeys.TTLPlatformAccess.Seconds()),
	}, nil
}

func (s *Service) Logout(ctx context.Context, userID, jti string) error {
	if s.rdb == nil || jti == "" {
		return nil
	}

	ttl, err := s.rdb.TTL(ctx, rediskeys.PlatformSessionKey(jti)).Result()
	if err != nil || ttl <= 0 {
		ttl = rediskeys.TTLPlatformAccess
	}

	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, rediskeys.PlatformSessionKey(jti))
	pipe.Set(ctx, rediskeys.PlatformBlacklistJTIKey(jti), "1", ttl)
	if userID != "" {
		pipe.SRem(ctx, rediskeys.PlatformUserSessionsKey(userID), jti)
	}
	_, err = pipe.Exec(ctx)
	return err
}

type MeProfile struct {
	User        map[string]any      `json:"user"`
	Roles       []string            `json:"roles"`
	Permissions []string            `json:"permissions"`
	Menus       []menu.AntDMenuNode `json:"menus"`
}

func (s *Service) GetMe(ctx context.Context, userID string, permissions []string) (*MeProfile, error) {
	var user iam.PlatformUser
	if err := s.db.WithContext(ctx).Preload("Roles").First(&user, "id = ?", userID).Error; err != nil {
		return nil, err
	}

	roleCodes := make([]string, 0, len(user.Roles))
	for _, r := range user.Roles {
		roleCodes = append(roleCodes, r.Code)
	}

	menus, err := s.menuService.GetFilteredMenuTree(ctx, permissions)
	if err != nil {
		return nil, err
	}

	if permissions == nil {
		permissions = []string{}
	}

	return &MeProfile{
		User: map[string]any{
			"id":          user.ID,
			"email":       user.Email,
			"full_name":   user.FullName,
			"avatar_url":  user.AvatarURL,
			"mfa_enabled": user.MFAEnabled,
		},
		Roles:       roleCodes,
		Permissions: permissions,
		Menus:       menus,
	}, nil
}

type MFASetupOutput struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
	Issuer     string `json:"issuer"`
	Account    string `json:"account"`
}

// SetupMFA generates a new TOTP secret and returns the otpauth:// URL for QR code rendering.
func (s *Service) SetupMFA(ctx context.Context, userID string) (*MFASetupOutput, error) {
	var user iam.PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", userID).Error; err != nil {
		return nil, err
	}
	if user.MFAEnabled {
		return nil, ErrMFAAlreadyEnabled
	}

	secret, err := GenerateMFASecret()
	if err != nil {
		return nil, err
	}

	if err := s.db.WithContext(ctx).Model(&user).Updates(map[string]any{
		"mfa_secret":  secret,
		"mfa_enabled": false,
		"updated_at":  time.Now(),
	}).Error; err != nil {
		return nil, err
	}

	issuer := "SchoolERP-Platform"
	otpAuthURL := BuildOTPAuthURL(issuer, user.Email, secret)

	return &MFASetupOutput{
		Secret:     secret,
		OTPAuthURL: otpAuthURL,
		Issuer:     issuer,
		Account:    user.Email,
	}, nil
}

// EnableMFA verifies the first 6-digit TOTP code from the user's Authenticator app and activates MFA.
func (s *Service) EnableMFA(ctx context.Context, userID, otpCode, ip, ua string) error {
	var user iam.PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", userID).Error; err != nil {
		return err
	}
	if user.MFAEnabled {
		return ErrMFAAlreadyEnabled
	}
	if user.MFASecret == nil || *user.MFASecret == "" {
		return ErrMFANotSetup
	}

	if !VerifyTOTP(*user.MFASecret, otpCode) {
		return ErrMFAInvalid
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user).Updates(map[string]any{
			"mfa_enabled": true,
			"updated_at":  time.Now(),
		}).Error; err != nil {
			return err
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:    "PLATFORM",
				ActorID:      &user.ID,
				Action:       "PLATFORM_MFA_ENABLED",
				ResourceType: "PLATFORM_USER",
				ResourceID:   &user.ID,
				BeforeData:   map[string]any{"mfa_enabled": false},
				AfterData:    map[string]any{"mfa_enabled": true},
				IPAddress:    ip,
				UserAgent:    ua,
			})
		}
		return nil
	})
}

// DisableMFA verifies user's password and current TOTP code before disabling MFA.
func (s *Service) DisableMFA(ctx context.Context, userID, password, otpCode, ip, ua string) error {
	var user iam.PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", userID).Error; err != nil {
		return err
	}
	if !user.MFAEnabled {
		return ErrMFANotEnabled
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}

	secret := ""
	if user.MFASecret != nil {
		secret = *user.MFASecret
	}
	if !VerifyTOTP(secret, otpCode) {
		return ErrMFAInvalid
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user).Updates(map[string]any{
			"mfa_enabled": false,
			"mfa_secret":  nil,
			"updated_at":  time.Now(),
		}).Error; err != nil {
			return err
		}

		if s.auditor != nil {
			return s.auditor.Log(tx, audit.AuditEntry{
				ActorType:    "PLATFORM",
				ActorID:      &user.ID,
				Action:       "PLATFORM_MFA_DISABLED",
				ResourceType: "PLATFORM_USER",
				ResourceID:   &user.ID,
				BeforeData:   map[string]any{"mfa_enabled": true},
				AfterData:    map[string]any{"mfa_enabled": false},
				IPAddress:    ip,
				UserAgent:    ua,
			})
		}
		return nil
	})
}
