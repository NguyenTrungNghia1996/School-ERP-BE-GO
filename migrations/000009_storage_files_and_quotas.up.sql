-- Tenant-scoped S3 / Cloudflare R2 image storage & quota tracking (PostgreSQL 17)

CREATE TABLE IF NOT EXISTS organization_storage_usages (
    organization_id UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
    file_count BIGINT NOT NULL DEFAULT 0 CHECK (file_count >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS stored_files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    school_id UUID REFERENCES schools(id) ON DELETE SET NULL,
    uploaded_by UUID,
    folder VARCHAR(100) NOT NULL DEFAULT 'general',
    object_key TEXT NOT NULL UNIQUE,
    original_name VARCHAR(255) NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    storage_provider VARCHAR(30) NOT NULL DEFAULT 'R2' CHECK (storage_provider IN ('R2', 'S3', 'MINIO')),
    bucket VARCHAR(120) NOT NULL,
    public_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_stored_files_org_created
    ON stored_files (organization_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_stored_files_org_folder
    ON stored_files (organization_id, folder)
    WHERE deleted_at IS NULL;

-- Seed quyền quản lý media / dung lượng lưu trữ và gán cho ROOT_ADMIN
INSERT INTO platform_permissions (code, module, name, description) VALUES
('platform:media:read', 'ORGANIZATION', 'Xem thư viện ảnh và dung lượng lưu trữ của Tổ chức', 'Xem danh sách ảnh và thống kê dung lượng R2/S3 của từng đơn vị'),
('platform:media:manage', 'ORGANIZATION', 'Tải lên và xóa ảnh của Tổ chức', 'Upload và xóa tệp ảnh trên thư mục riêng của từng đơn vị')
ON CONFLICT (LOWER(code)) DO NOTHING;

INSERT INTO platform_role_permissions (platform_role_id, platform_permission_id)
SELECT '00000000-0000-0000-0000-000000000001', id
FROM platform_permissions
WHERE code IN ('platform:media:read', 'platform:media:manage')
ON CONFLICT DO NOTHING;
