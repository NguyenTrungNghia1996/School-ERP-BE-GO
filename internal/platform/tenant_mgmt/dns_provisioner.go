package tenant_mgmt

import (
	"context"
	"errors"
	"log"
	"school-erp/internal/pkg/cloudflare"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/audit"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type DNSProvisioner struct {
	db      *gorm.DB
	rdb     *redis.Client
	cf      *cloudflare.Client
	auditor *audit.Service
	jobCh   chan uuid.UUID
}

func NewDNSProvisioner(db *gorm.DB, rdb *redis.Client, cf *cloudflare.Client, auditor *audit.Service) *DNSProvisioner {
	p := &DNSProvisioner{
		db:      db,
		rdb:     rdb,
		cf:      cf,
		auditor: auditor,
		jobCh:   make(chan uuid.UUID, 256),
	}
	go p.workerLoop()
	go p.cleanupLoop()
	return p
}

// EnqueueProvision đẩy yêu cầu cấp phát/đồng bộ DNS của một Organization vào hàng đợi bất đồng bộ.
func (p *DNSProvisioner) EnqueueProvision(orgID uuid.UUID) {
	select {
	case p.jobCh <- orgID:
	default:
		go p.ProvisionOrgDNS(context.Background(), orgID)
	}
}

func (p *DNSProvisioner) workerLoop() {
	for orgID := range p.jobCh {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		p.ProvisionOrgDNS(ctx, orgID)
		cancel()
	}
}

// ProvisionOrgDNS thực thi tạo bản ghi CNAME trên Cloudflare với cơ chế retry backoff (2s, 5s, 15s).
func (p *DNSProvisioner) ProvisionOrgDNS(ctx context.Context, orgID uuid.UUID) {
	var org Organization
	if err := p.db.WithContext(ctx).First(&org, "id = ?", orgID).Error; err != nil {
		return
	}

	if p.cf == nil || !p.cf.IsConfigured() {
		errMsg := cloudflare.ErrNotConfigured.Error()
		_ = p.db.WithContext(ctx).Model(&Organization{}).Where("id = ?", org.ID).Updates(map[string]any{
			"dns_status":     "FAILED",
			"dns_last_error": errMsg,
			"updated_at":     time.Now(),
		}).Error
		return
	}

	backoffDurations := []time.Duration{0, 2 * time.Second, 5 * time.Second, 15 * time.Second}
	var lastErr error
	var recordID string

	for attempt, wait := range backoffDurations {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}

		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		recordID, lastErr = p.cf.CreateSubdomainCNAME(reqCtx, org.Subdomain, org.ID.String())
		cancel()

		if lastErr == nil && recordID != "" {
			break
		}

		if errors.Is(lastErr, cloudflare.ErrNotConfigured) {
			break
		}
		log.Printf("⚠️ [Cloudflare DNS] Thử lần %d cho subdomain '%s' thất bại: %v\n", attempt+1, org.Subdomain, lastErr)
	}

	now := time.Now()
	if lastErr != nil {
		errMsg := lastErr.Error()
		_ = p.db.WithContext(ctx).Model(&Organization{}).Where("id = ?", org.ID).Updates(map[string]any{
			"dns_status":     "FAILED",
			"dns_last_error": errMsg,
			"updated_at":     now,
		}).Error
		return
	}

	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Organization{}).Where("id = ?", org.ID).Updates(map[string]any{
			"cloudflare_dns_record_id": recordID,
			"dns_status":               "ACTIVE",
			"dns_last_error":           nil,
			"dns_synced_at":            now,
			"updated_at":               now,
		}).Error; err != nil {
			return err
		}

		if p.auditor != nil {
			return p.auditor.Log(tx, audit.AuditEntry{
				ActorType:      "SYSTEM",
				OrganizationID: &org.ID,
				Action:         "CLOUDFLARE_DNS_PROVISIONED",
				ResourceType:   "ORGANIZATION",
				ResourceID:     &org.ID,
				AfterData: map[string]any{
					"subdomain":                org.Subdomain,
					"cloudflare_dns_record_id": recordID,
					"dns_status":               "ACTIVE",
				},
			})
		}
		return nil
	})
	if err != nil {
		log.Printf("⚠️ [Cloudflare DNS] Lỗi lưu trạng thái ACTIVE cho Org %s: %v\n", org.ID, err)
		return
	}

	if p.rdb != nil {
		_ = p.rdb.Del(ctx, rediskeys.TenantSubdomainKey(org.Subdomain)).Err()
	}
	log.Printf("✅ [Cloudflare DNS] Đã cấp phát CNAME cho '%s' (RecordID: %s)\n", org.Subdomain, recordID)
}

// CleanupExpiredRedirects quét và xóa các bản ghi CNAME cũ đã hết thời hạn redirect_until trên Cloudflare.
func (p *DNSProvisioner) CleanupExpiredRedirects(ctx context.Context) int {
	if p.cf == nil || !p.cf.IsConfigured() {
		return 0
	}

	var expiredHistories []OrganizationSubdomainHistory
	now := time.Now()
	if err := p.db.WithContext(ctx).
		Where("old_record_status = 'REDIRECTING' AND redirect_until IS NOT NULL AND redirect_until <= ?", now).
		Find(&expiredHistories).Error; err != nil {
		return 0
	}

	cleaned := 0
	for _, h := range expiredHistories {
		recID := ""
		if h.OldCloudflareRecordID != nil {
			recID = *h.OldCloudflareRecordID
		}

		if err := p.cf.DeleteRecord(ctx, recID); err != nil {
			_ = p.db.WithContext(ctx).Model(&OrganizationSubdomainHistory{}).
				Where("id = ?", h.ID).
				Update("old_record_status", "CLEANUP_FAILED").Error
			continue
		}

		cleanTime := time.Now()
		_ = p.db.WithContext(ctx).Model(&OrganizationSubdomainHistory{}).
			Where("id = ?", h.ID).
			Updates(map[string]any{
				"old_record_status": "DELETED",
				"cleaned_up_at":     cleanTime,
			}).Error

		if p.rdb != nil {
			_ = p.rdb.Del(ctx, rediskeys.TenantSubdomainKey(h.OldSubdomain)).Err()
		}
		cleaned++
	}
	return cleaned
}

func (p *DNSProvisioner) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		p.CleanupExpiredRedirects(ctx)
		cancel()
	}
}
