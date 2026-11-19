-- Tenant Core Schema & PostgreSQL Row Level Security (SPEC-BE-TENANT-CORE v1.0)

-- 1. Mở rộng bảng organizations
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS logo_url TEXT;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_status_check;
ALTER TABLE organizations ADD CONSTRAINT organizations_status_check
    CHECK (status IN ('PENDING_APPROVAL', 'ACTIVE', 'SUSPENDED', 'ARCHIVED'));

-- 2. Mở rộng bảng schools & bổ sung UNIQUE (organization_id, id) phục vụ Composite FK
ALTER TABLE schools ADD COLUMN IF NOT EXISTS education_levels JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE schools ADD COLUMN IF NOT EXISTS logo_url TEXT;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_schools_org_id_id') THEN
        ALTER TABLE schools ADD CONSTRAINT uq_schools_org_id_id UNIQUE (organization_id, id);
    END IF;
END $$;

-- 3. Mở rộng bảng users (hỗ trợ đăng nhập bằng email / username / phone)
ALTER TABLE users ADD COLUMN IF NOT EXISTS username VARCHAR(100);
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_username
    ON users (LOWER(username))
    WHERE username IS NOT NULL AND deleted_at IS NULL;

-- 4. Mở rộng bảng school_memberships (member_type, joined_at, Composite FK & UNIQUE)
ALTER TABLE school_memberships ADD COLUMN IF NOT EXISTS member_type VARCHAR(30) NOT NULL DEFAULT 'STAFF';
ALTER TABLE school_memberships DROP CONSTRAINT IF EXISTS school_memberships_member_type_check;
ALTER TABLE school_memberships ADD CONSTRAINT school_memberships_member_type_check
    CHECK (member_type IN ('SCHOOL_ADMIN', 'TEACHER', 'STAFF', 'STUDENT', 'GUARDIAN'));
ALTER TABLE school_memberships ADD COLUMN IF NOT EXISTS joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
UPDATE school_memberships SET member_type = 'SCHOOL_ADMIN' WHERE is_school_admin = TRUE AND member_type = 'STAFF';
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_school_memberships_org_id_id') THEN
        ALTER TABLE school_memberships ADD CONSTRAINT uq_school_memberships_org_id_id UNIQUE (organization_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_school_memberships_org_school') THEN
        ALTER TABLE school_memberships ADD CONSTRAINT fk_school_memberships_org_school
            FOREIGN KEY (organization_id, school_id) REFERENCES schools(organization_id, id) ON DELETE CASCADE;
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_memberships_user_status ON school_memberships (user_id, status);
CREATE INDEX IF NOT EXISTS idx_memberships_org_school ON school_memberships (organization_id, school_id);

-- 5. Bảng Vai trò của Đơn vị / Trường (Custom RBAC)
CREATE TABLE IF NOT EXISTS roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    school_id UUID,
    code VARCHAR(60) NOT NULL,
    name VARCHAR(120) NOT NULL,
    description TEXT,
    data_scope VARCHAR(30) NOT NULL DEFAULT 'SCHOOL'
        CHECK (data_scope IN ('ORGANIZATION', 'SCHOOL', 'DEPARTMENT', 'ASSIGNED_CLASS', 'SELF')),
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (organization_id, school_id) REFERENCES schools(organization_id, id) ON DELETE CASCADE,
    UNIQUE (organization_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_roles_org_school_code
    ON roles (organization_id, COALESCE(school_id, '00000000-0000-0000-0000-000000000000'::uuid), UPPER(code));

-- 6. Bảng Gán Permission từ Catalog vào Role của Đơn vị
CREATE TABLE IF NOT EXISTS role_permissions (
    organization_id UUID NOT NULL,
    role_id UUID NOT NULL,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (role_id, permission_id),
    FOREIGN KEY (organization_id, role_id) REFERENCES roles(organization_id, id) ON DELETE CASCADE
);

-- 7. Bảng Gán Role cho Thành viên (1 Membership có thể giữ nhiều Role)
CREATE TABLE IF NOT EXISTS user_roles (
    organization_id UUID NOT NULL,
    membership_id UUID NOT NULL,
    role_id UUID NOT NULL,
    assigned_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (membership_id, role_id),
    FOREIGN KEY (organization_id, membership_id) REFERENCES school_memberships(organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, role_id) REFERENCES roles(organization_id, id) ON DELETE CASCADE
);

-- 8. Bảng Tùy chỉnh Menu của Đơn vị (Override menu nền & thêm menu riêng)
CREATE TABLE IF NOT EXISTS organization_menu_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    base_menu_id UUID REFERENCES menus(id) ON DELETE CASCADE,
    parent_config_id UUID REFERENCES organization_menu_configs(id) ON DELETE CASCADE,
    custom_label VARCHAR(120),
    custom_icon VARCHAR(60),
    custom_route_id UUID REFERENCES tenant_routes(id) ON DELETE RESTRICT,
    sort_order INT NOT NULL DEFAULT 0,
    is_visible BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, base_menu_id)
);

-- 9. Seed Core Features & Permissions cho Tenant Core (CORE_ORG, CORE_IAM)
INSERT INTO features (id, code, module, name, description, is_core, is_active) VALUES
('550e8400-e29b-41d4-a716-446655440010', 'CORE_ORG', 'ORGANIZATION', 'Quản lý Đơn vị & Trường học', 'Cài đặt thông tin tổ chức, danh sách trường trực thuộc và menu đơn vị', TRUE, TRUE),
('550e8400-e29b-41d4-a716-446655440011', 'CORE_IAM', 'IAM', 'Quản lý Nhân sự & Phân quyền Đơn vị', 'Quản lý thành viên trường học, vai trò tùy chỉnh và phân quyền RBAC', TRUE, TRUE)
ON CONFLICT (UPPER(code)) DO NOTHING;

INSERT INTO permissions (feature_id, code, resource, action, name, description, is_sensitive) VALUES
((SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'org:settings:update', 'org_settings', 'update', 'Cập nhật thông tin đơn vị', 'Chỉnh sửa thông tin liên hệ, logo và cấu hình đơn vị', FALSE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'org:menu:manage', 'org_menu', 'manage', 'Tùy chỉnh menu đơn vị', 'Sắp xếp, đổi tên/icon và tạo menu riêng của đơn vị', FALSE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'school:read', 'school', 'read', 'Xem danh sách trường học', 'Xem danh sách các trường trực thuộc đơn vị', FALSE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'school:create', 'school', 'create', 'Tạo trường học mới', 'Khởi tạo trường học trực thuộc đơn vị trong hạn mức gói', FALSE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'school:update', 'school', 'update', 'Cập nhật trường học', 'Chỉnh sửa thông tin trường học trực thuộc', FALSE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), 'iam:member:read', 'iam_member', 'read', 'Xem danh sách thành viên trường', 'Xem danh sách tài khoản và vai trò tại trường', FALSE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), 'iam:member:create', 'iam_member', 'create', 'Thêm thành viên vào trường', 'Tạo tài khoản hoặc gắn thành viên vào trường học', FALSE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), 'iam:member:assign_role', 'iam_member', 'assign_role', 'Phân vai trò thành viên', 'Gán hoặc thu hồi vai trò cho thành viên trường', TRUE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), 'iam:role:read', 'iam_role', 'read', 'Xem vai trò và quyền khả dụng', 'Xem danh sách vai trò và các quyền thuộc gói của đơn vị', FALSE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), 'iam:role:manage', 'iam_role', 'manage', 'Quản lý vai trò tùy chỉnh', 'Tạo và cập nhật vai trò tùy chỉnh của đơn vị', TRUE)
ON CONFLICT (LOWER(code)) DO NOTHING;

-- Gắn CORE_ORG và CORE_IAM vào gói PRIMARY mẫu (nếu tồn tại)
INSERT INTO service_package_features (package_version_id, feature_id)
SELECT '770e8400-e29b-41d4-a716-446655440098', id
FROM features
WHERE UPPER(code) IN ('CORE_ORG', 'CORE_IAM')
  AND EXISTS (SELECT 1 FROM service_package_versions WHERE id = '770e8400-e29b-41d4-a716-446655440098')
ON CONFLICT DO NOTHING;

-- ============================================================================
-- 10. KÍCH HOẠT POSTGRESQL ROW LEVEL SECURITY (RLS) CHO TOÀN BỘ BẢNG TENANT-SCOPED
-- ============================================================================
DO $$
DECLARE
    tbl TEXT;
BEGIN
    FOREACH tbl IN ARRAY ARRAY[
        'schools',
        'school_memberships',
        'roles',
        'role_permissions',
        'user_roles',
        'organization_menu_configs'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', tbl);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY;', tbl);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation_%I ON %I;', tbl, tbl);
        EXECUTE format('
            CREATE POLICY tenant_isolation_%I ON %I
            USING (
                current_setting(''app.bypass_rls'', TRUE) = ''on''
                OR NULLIF(current_setting(''app.organization_id'', TRUE), '''') IS NULL
                OR organization_id = NULLIF(current_setting(''app.organization_id'', TRUE), '''')::UUID
            )
            WITH CHECK (
                current_setting(''app.bypass_rls'', TRUE) = ''on''
                OR NULLIF(current_setting(''app.organization_id'', TRUE), '''') IS NULL
                OR organization_id = NULLIF(current_setting(''app.organization_id'', TRUE), '''')::UUID
            );
        ', tbl, tbl);
    END LOOP;
END $$;
