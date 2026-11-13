-- 1. Bổ sung trường quản lý Cloudflare DNS trên bảng organizations
ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS cloudflare_dns_record_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS dns_status VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CHECK (dns_status IN ('PENDING', 'PROVISIONING', 'ACTIVE', 'FAILED', 'DELETING')),
    ADD COLUMN IF NOT EXISTS dns_last_error TEXT,
    ADD COLUMN IF NOT EXISTS dns_synced_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_organizations_dns_status
    ON organizations (dns_status)
    WHERE dns_status IN ('PROVISIONING', 'FAILED');

-- 2. Bổ sung tracking Cloudflare cho lịch sử đổi subdomain (hỗ trợ Redirect 301/308 subdomain cũ)
ALTER TABLE organization_subdomain_histories
    ADD COLUMN IF NOT EXISTS old_cloudflare_record_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS old_record_status VARCHAR(20) NOT NULL DEFAULT 'REDIRECTING'
        CHECK (old_record_status IN ('REDIRECTING', 'DELETED', 'CLEANUP_FAILED')),
    ADD COLUMN IF NOT EXISTS cleaned_up_at TIMESTAMPTZ;
