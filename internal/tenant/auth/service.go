package auth

import (
	"context"
	"errors"
	"school-erp/internal/pkg/database"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/billing"
	tenantmenu "school-erp/internal/tenant/menu"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrOrgNotFoundOrInactive = errors.New("organization not found or inactive")
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrAccountLocked         = errors.New("account is locked or inactive")
	ErrNoMembershipInOrg     = errors.New("user has no active membership in this organization")
	ErrSchoolAccessDenied    = errors.New("user has no active membership in this school")
	ErrSessionExpired        = errors.New("session expired or revoked")
)

type Service struct {
	db         *gorm.DB
	rdb        *redis.Client
	billingSvc *billing.Service
	menuSvc    *tenantmenu.Service
}

func NewService(db *gorm.DB, rdb *redis.Client, billingSvc *billing.Service, menuSvc *tenantmenu.Service) *Service {
	return &Service{
		db:         db,
		rdb:        rdb,
		billingSvc: billingSvc,
		menuSvc:    menuSvc,
	}
}

type TenantLoginInput struct {
	Subdomain  string `json:"subdomain" example:"thpt-chu-van-an"`
	Identifier string `json:"identifier" example:"admin@chuvanan.edu.vn"`
	Password   string `json:"password" example:"Admin@123456"`
}

type TenantLoginOutput struct {
	AccessToken    string `json:"access_token"`
	RefreshToken   string `json:"refresh_token"`
	TokenType      string `json:"token_type" example:"Bearer"`
	ExpiresIn      int    `json:"expires_in" example:"900"`
	OrganizationID string `json:"organization_id"`
	ActiveSchoolID string `json:"active_school_id"`
}

type TenantRefreshInput struct {
	RefreshToken string `json:"refresh_token"`
}

type SwitchSchoolInput struct {
	SchoolID string `json:"school_id"`
}

type MeSchoolItem struct {
	ID           uuid.UUID `json:"id"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	MembershipID uuid.UUID `json:"membership_id"`
	MemberType   string    `json:"member_type"`
}

type TenantMeOutput struct {
	User           map[string]any               `json:"user"`
	Organization   map[string]any               `json:"organization"`
	Schools        []MeSchoolItem               `json:"schools"`
	ActiveSchoolID string                       `json:"active_school_id"`
	Roles          []string                     `json:"roles"`
	Permissions    []string                     `json:"permissions"`
	Menus          []tenantmenu.DynamicMenuNode `json:"menus"`
	IsSupportMode  bool                         `json:"is_support_mode"`
}

func (s *Service) Login(ctx context.Context, input TenantLoginInput) (*TenantLoginOutput, error) {
	subdomain := strings.ToLower(strings.TrimSpace(input.Subdomain))
	identifier := strings.ToLower(strings.TrimSpace(input.Identifier))

	// 1. Xác minh Organization ACTIVE
	var org struct {
		ID     uuid.UUID `gorm:"column:id"`
		Status string    `gorm:"column:status"`
	}
	if err := s.db.WithContext(ctx).Table("organizations").
		Select("id, status").
		Where("LOWER(subdomain) = ? AND deleted_at IS NULL", subdomain).
		First(&org).Error; err != nil || org.Status != "ACTIVE" {
		return nil, ErrOrgNotFoundOrInactive
	}

	// 2. Tìm user theo email / username / phone
	var user struct {
		ID           uuid.UUID `gorm:"column:id"`
		PasswordHash string    `gorm:"column:password_hash"`
		Status       string    `gorm:"column:status"`
	}
	if err := s.db.WithContext(ctx).Table("users").
		Select("id, password_hash, status").
		Where("deleted_at IS NULL AND (LOWER(email) = ? OR LOWER(username) = ? OR phone = ?)", identifier, identifier, strings.TrimSpace(input.Identifier)).
		First(&user).Error; err != nil {
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	if user.Status != "ACTIVE" {
		return nil, ErrAccountLocked
	}

	// 3. Xác minh user có membership ACTIVE tại Organization này (qua WithTenantTx)
	var membership struct {
		ID       uuid.UUID `gorm:"column:id"`
		SchoolID uuid.UUID `gorm:"column:school_id"`
	}
	tc := database.TenantContext{OrganizationID: org.ID.String()}
	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		return tx.Table("school_memberships").
			Select("id, school_id").
			Where("user_id = ? AND organization_id = ? AND status = 'ACTIVE'", user.ID, org.ID).
			Order("created_at ASC").
			First(&membership).Error
	})
	if err != nil {
		return nil, ErrNoMembershipInOrg
	}

	jti := uuid.New().String()
	userIDStr := user.ID.String()
	orgIDStr := org.ID.String()
	schoolIDStr := membership.SchoolID.String()

	accessToken, err := GenerateTenantToken(userIDStr, orgIDStr, schoolIDStr, jti, "access", rediskeys.TTLTenantAccess)
	if err != nil {
		return nil, err
	}
	refreshToken, err := GenerateTenantToken(userIDStr, orgIDStr, schoolIDStr, jti, "refresh", rediskeys.TTLTenantSession)
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		pipe := s.rdb.Pipeline()
		pipe.Set(ctx, rediskeys.TenantSessionKey(jti), userIDStr, rediskeys.TTLTenantSession)
		pipe.SAdd(ctx, rediskeys.TenantUserSessionsKey(userIDStr), jti)
		pipe.Expire(ctx, rediskeys.TenantUserSessionsKey(userIDStr), rediskeys.TTLTenantSession)
		pipe.SAdd(ctx, rediskeys.TenantUserOrgsKey(userIDStr), orgIDStr)
		pipe.Expire(ctx, rediskeys.TenantUserOrgsKey(userIDStr), rediskeys.TTLTenantUserOrgs)
		_, _ = pipe.Exec(ctx)
	}

	_ = s.db.WithContext(ctx).Table("users").Where("id = ?", user.ID).Update("last_login_at", time.Now()).Error

	return &TenantLoginOutput{
		AccessToken:    accessToken,
		RefreshToken:   refreshToken,
		TokenType:      "Bearer",
		ExpiresIn:      int(rediskeys.TTLTenantAccess.Seconds()),
		OrganizationID: orgIDStr,
		ActiveSchoolID: schoolIDStr,
	}, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (*TenantLoginOutput, error) {
	claims, err := ParseTenantRefreshToken(strings.TrimSpace(refreshToken))
	if err != nil {
		return nil, err
	}

	if s.rdb != nil {
		if bl, err := s.rdb.Exists(ctx, rediskeys.TenantBlacklistJTIKey(claims.JTI)).Result(); err == nil && bl > 0 {
			return nil, ErrSessionExpired
		}
		if ex, err := s.rdb.Exists(ctx, rediskeys.TenantSessionKey(claims.JTI)).Result(); err == nil && ex == 0 {
			return nil, ErrSessionExpired
		}
	}

	var status string
	if err := s.db.WithContext(ctx).Table("users").Select("status").Where("id = ? AND deleted_at IS NULL", claims.UserID).Scan(&status).Error; err != nil || status != "ACTIVE" {
		return nil, ErrAccountLocked
	}

	accessToken, err := GenerateTenantToken(claims.UserID, claims.OrgID, claims.SchoolID, claims.JTI, "access", rediskeys.TTLTenantAccess)
	if err != nil {
		return nil, err
	}

	return &TenantLoginOutput{
		AccessToken:    accessToken,
		RefreshToken:   refreshToken,
		TokenType:      "Bearer",
		ExpiresIn:      int(rediskeys.TTLTenantAccess.Seconds()),
		OrganizationID: claims.OrgID,
		ActiveSchoolID: claims.SchoolID,
	}, nil
}

func (s *Service) Logout(ctx context.Context, userID, jti string) error {
	if s.rdb == nil || jti == "" {
		return nil
	}
	ttl, err := s.rdb.TTL(ctx, rediskeys.TenantSessionKey(jti)).Result()
	if err != nil || ttl <= 0 {
		ttl = rediskeys.TTLTenantAccess
	}
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, rediskeys.TenantSessionKey(jti))
	pipe.Set(ctx, rediskeys.TenantBlacklistJTIKey(jti), "1", ttl)
	if userID != "" {
		pipe.SRem(ctx, rediskeys.TenantUserSessionsKey(userID), jti)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (s *Service) SwitchSchool(ctx context.Context, tc database.TenantContext, userID, targetSchoolID, jti string) (*TenantLoginOutput, error) {
	if _, err := uuid.Parse(targetSchoolID); err != nil {
		return nil, ErrSchoolAccessDenied
	}

	var membershipID uuid.UUID
	err := database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
		return tx.Table("school_memberships").
			Select("id").
			Where("user_id = ? AND organization_id = ? AND school_id = ? AND status = 'ACTIVE'", userID, tc.OrganizationID, targetSchoolID).
			Scan(&membershipID).Error
	})
	if err != nil || membershipID == uuid.Nil {
		return nil, ErrSchoolAccessDenied
	}

	accessToken, err := GenerateTenantToken(userID, tc.OrganizationID, targetSchoolID, jti, "access", rediskeys.TTLTenantAccess)
	if err != nil {
		return nil, err
	}

	return &TenantLoginOutput{
		AccessToken:    accessToken,
		TokenType:      "Bearer",
		ExpiresIn:      int(rediskeys.TTLTenantAccess.Seconds()),
		OrganizationID: tc.OrganizationID,
		ActiveSchoolID: targetSchoolID,
	}, nil
}

func (s *Service) GetMe(ctx context.Context, tc database.TenantContext, userID, membershipID string, isSupport bool) (*TenantMeOutput, error) {
	orgID := uuid.MustParse(tc.OrganizationID)

	var org map[string]any
	if err := s.db.WithContext(ctx).Table("organizations").
		Select("id, code, name, subdomain, logo_url, status").
		Where("id = ?", orgID).
		Take(&org).Error; err != nil {
		return nil, err
	}

	userMap := map[string]any{"id": userID}
	if !isSupport {
		var u struct {
			ID                 uuid.UUID `gorm:"column:id"`
			Email              *string   `gorm:"column:email"`
			Username           *string   `gorm:"column:username"`
			FullName           string    `gorm:"column:full_name"`
			Phone              *string   `gorm:"column:phone"`
			AvatarURL          *string   `gorm:"column:avatar_url"`
			MustChangePassword bool      `gorm:"column:must_change_password"`
		}
		if err := s.db.WithContext(ctx).Table("users").Where("id = ?", userID).First(&u).Error; err == nil {
			userMap = map[string]any{
				"id":                   u.ID,
				"email":                u.Email,
				"username":             u.Username,
				"full_name":            u.FullName,
				"phone":                u.Phone,
				"avatar_url":           u.AvatarURL,
				"must_change_password": u.MustChangePassword,
			}
		}
	}

	var schools []MeSchoolItem
	var roleCodes []string
	permMap := make(map[string]string)

	if !isSupport {
		_ = database.WithTenantTx(ctx, s.db, tc, func(tx *gorm.DB) error {
			_ = tx.Raw(`
				SELECT s.id, s.code, s.name, s.status, sm.id AS membership_id, sm.member_type
				FROM school_memberships sm
				JOIN schools s ON s.id = sm.school_id AND s.deleted_at IS NULL
				WHERE sm.user_id = ? AND sm.organization_id = ? AND sm.status = 'ACTIVE'
				ORDER BY sm.created_at ASC
			`, userID, orgID).Scan(&schools).Error

			if membershipID != "" {
				_ = tx.Raw(`
					SELECT r.code
					FROM user_roles ur
					JOIN roles r ON r.id = ur.role_id
					WHERE ur.membership_id = ?
				`, membershipID).Scan(&roleCodes).Error
			}
			return nil
		})

		// Load effective permissions intersected with entitlement
		var enabledFeatures []string
		if s.billingSvc != nil {
			if ent, err := s.billingSvc.CalculateEntitlement(ctx, orgID); err == nil {
				enabledFeatures = ent.EnabledFeatures
			}
		}
		featureSet := make(map[string]bool, len(enabledFeatures))
		for _, f := range enabledFeatures {
			featureSet[strings.ToUpper(f)] = true
		}

		if membershipID != "" {
			var rows []struct {
				PermCode    string `gorm:"column:perm_code"`
				FeatureCode string `gorm:"column:feature_code"`
				DataScope   string `gorm:"column:data_scope"`
			}
			_ = s.db.WithContext(ctx).Raw(`
				SELECT p.code AS perm_code, f.code AS feature_code, r.data_scope
				FROM user_roles ur
				JOIN roles r ON r.id = ur.role_id AND r.organization_id = ur.organization_id
				JOIN role_permissions rp ON rp.role_id = r.id AND rp.organization_id = r.organization_id
				JOIN permissions p ON p.id = rp.permission_id AND p.is_active = TRUE
				JOIN features f ON f.id = p.feature_id AND f.is_active = TRUE
				WHERE ur.membership_id = ?
			`, membershipID).Scan(&rows).Error

			for _, row := range rows {
				if len(featureSet) > 0 && !featureSet[strings.ToUpper(row.FeatureCode)] {
					continue
				}
				permMap[row.PermCode] = row.DataScope
			}
		}
	}

	if schools == nil {
		schools = []MeSchoolItem{}
	}
	if roleCodes == nil {
		roleCodes = []string{}
	}
	permList := make([]string, 0, len(permMap))
	for k := range permMap {
		permList = append(permList, k)
	}

	var menus []tenantmenu.DynamicMenuNode
	if s.menuSvc != nil {
		menus, _ = s.menuSvc.GetDynamicMenuTree(ctx, orgID, permMap)
	}
	if menus == nil {
		menus = []tenantmenu.DynamicMenuNode{}
	}

	return &TenantMeOutput{
		User:           userMap,
		Organization:   org,
		Schools:        schools,
		ActiveSchoolID: tc.SchoolID,
		Roles:          roleCodes,
		Permissions:    permList,
		Menus:          menus,
		IsSupportMode:  isSupport,
	}, nil
}
