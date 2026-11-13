ALTER TABLE organization_subdomain_histories
    DROP COLUMN IF EXISTS cleaned_up_at,
    DROP COLUMN IF EXISTS old_record_status,
    DROP COLUMN IF EXISTS old_cloudflare_record_id;

DROP INDEX IF EXISTS idx_organizations_dns_status;

ALTER TABLE organizations
    DROP COLUMN IF EXISTS dns_synced_at,
    DROP COLUMN IF EXISTS dns_last_error,
    DROP COLUMN IF EXISTS dns_status,
    DROP COLUMN IF EXISTS cloudflare_dns_record_id;
