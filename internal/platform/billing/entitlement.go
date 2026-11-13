package billing

import (
	"context"
	"encoding/json"
	"school-erp/internal/pkg/rediskeys"
	"time"

	"github.com/google/uuid"
)

// CalculateEntitlement computes or retrieves from Redis the merged OrganizationEntitlement with Smart TTL.
func (s *Service) CalculateEntitlement(ctx context.Context, orgID uuid.UUID) (*OrganizationEntitlement, error) {
	orgIDStr := orgID.String()
	cacheKey := rediskeys.TenantEntitlementKey(orgIDStr)

	if s.rdb != nil {
		cached, err := s.rdb.Get(ctx, cacheKey).Result()
		if err == nil && cached != "" {
			var ent OrganizationEntitlement
			if err := json.Unmarshal([]byte(cached), &ent); err == nil {
				return &ent, nil
			}
		}
	}

	now := time.Now()
	var subs []OrganizationSubscription
	if err := s.db.WithContext(ctx).
		Preload("PackageVersion.Package").
		Preload("PackageVersion.Features").
		Where("organization_id = ? AND status = 'ACTIVE' AND started_at <= ? AND expires_at > ?", orgID, now, now).
		Order("subscription_type DESC, started_at ASC").
		Find(&subs).Error; err != nil {
		return nil, err
	}

	var primaryPkg *ActivePackageSummary
	activeAddons := make([]ActivePackageSummary, 0)
	featureSet := make(map[string]bool)
	enabledFeatures := make([]string, 0)
	effectiveQuotas := make(map[string]int64)
	var nearestExpiry *time.Time

	for _, sub := range subs {
		if sub.PackageVersion == nil || sub.PackageVersion.Package == nil {
			continue
		}
		ver := sub.PackageVersion
		pkg := ver.Package

		summary := ActivePackageSummary{
			SubscriptionID: sub.ID,
			PackageCode:    pkg.Code,
			PackageName:    pkg.Name,
			VersionNumber:  ver.VersionNumber,
			ExpiresAt:      sub.ExpiresAt,
		}

		if sub.SubscriptionType == "PRIMARY" && primaryPkg == nil {
			sCopy := summary
			primaryPkg = &sCopy
		} else if sub.SubscriptionType == "ADDON" {
			activeAddons = append(activeAddons, summary)
		}

		if nearestExpiry == nil || sub.ExpiresAt.Before(*nearestExpiry) {
			expCopy := sub.ExpiresAt
			nearestExpiry = &expCopy
		}

		for _, feat := range ver.Features {
			if feat.IsActive && !featureSet[feat.Code] {
				featureSet[feat.Code] = true
				enabledFeatures = append(enabledFeatures, feat.Code)
			}
		}

		// Merge quotas from package version, overridden by custom_quotas on subscription
		subQuotas := parseQuotaMap(ver.Quotas)
		customQuotas := parseQuotaMap(sub.CustomQuotas)
		for k, v := range customQuotas {
			subQuotas[k] = v
		}

		for k, v := range subQuotas {
			if sub.SubscriptionType == "PRIMARY" {
				if v > effectiveQuotas[k] {
					effectiveQuotas[k] = v
				}
			} else {
				// Add-on cộng dồn hạn mức bổ sung (VD: thêm dung lượng lưu trữ hoặc thêm số học sinh)
				effectiveQuotas[k] += v
			}
		}
	}

	ent := &OrganizationEntitlement{
		OrganizationID:  orgIDStr,
		PrimaryPackage:  primaryPkg,
		ActiveAddons:    activeAddons,
		EnabledFeatures: enabledFeatures,
		EffectiveQuotas: effectiveQuotas,
		CalculatedAt:    now,
		NearestExpiryAt: nearestExpiry,
	}

	if s.rdb != nil {
		ttl := rediskeys.TTLTenantEntitlement
		if nearestExpiry != nil {
			untilExpiry := time.Until(*nearestExpiry)
			if untilExpiry > 0 && untilExpiry < ttl {
				ttl = untilExpiry
			}
		}
		if raw, err := json.Marshal(ent); err == nil && ttl > 0 {
			_ = s.rdb.Set(ctx, cacheKey, string(raw), ttl).Err()
		}
	}

	return ent, nil
}

func parseQuotaMap(raw json.RawMessage) map[string]int64 {
	res := make(map[string]int64)
	if len(raw) == 0 {
		return res
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return res
	}
	for k, v := range m {
		switch val := v.(type) {
		case float64:
			res[k] = int64(val)
		case int64:
			res[k] = val
		case int:
			res[k] = int64(val)
		}
	}
	return res
}

// InvalidateOrgEntitlementCache deletes tenant:entitlement:{org_id} and tenant:menus:{org_id}:* on Redis.
func (s *Service) InvalidateOrgEntitlementCache(ctx context.Context, orgID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	orgIDStr := orgID.String()
	_ = s.rdb.Del(ctx, rediskeys.TenantEntitlementKey(orgIDStr)).Err()

	pattern := rediskeys.TenantMenusPattern(orgIDStr)
	keys, err := s.rdb.Keys(ctx, pattern).Result()
	if err == nil && len(keys) > 0 {
		_ = s.rdb.Del(ctx, keys...).Err()
	}
}

// ProcessSubscriptionExpirations transitions expired ACTIVE subscriptions to EXPIRED and activates due SCHEDULED subscriptions.
func (s *Service) ProcessSubscriptionExpirations(ctx context.Context) {
	now := time.Now()

	// 1. Activate SCHEDULED subscriptions whose started_at <= now < expires_at
	var scheduledSubs []OrganizationSubscription
	if err := s.db.WithContext(ctx).
		Where("status = 'SCHEDULED' AND started_at <= ? AND expires_at > ?", now, now).
		Find(&scheduledSubs).Error; err == nil {
		for _, sub := range scheduledSubs {
			_ = s.db.WithContext(ctx).Model(&OrganizationSubscription{}).
				Where("id = ?", sub.ID).
				Updates(map[string]any{"status": "ACTIVE", "updated_at": now}).Error
			s.InvalidateOrgEntitlementCache(ctx, sub.OrganizationID)
		}
	}

	// 2. Expire ACTIVE subscriptions whose expires_at <= now
	var expiredSubs []OrganizationSubscription
	if err := s.db.WithContext(ctx).
		Where("status = 'ACTIVE' AND expires_at <= ?", now).
		Find(&expiredSubs).Error; err == nil {
		for _, sub := range expiredSubs {
			_ = s.db.WithContext(ctx).Model(&OrganizationSubscription{}).
				Where("id = ?", sub.ID).
				Updates(map[string]any{"status": "EXPIRED", "updated_at": now}).Error
			s.InvalidateOrgEntitlementCache(ctx, sub.OrganizationID)
		}
	}
}

func (s *Service) expirationWorkerLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		s.ProcessSubscriptionExpirations(ctx)
		cancel()
	}
}
