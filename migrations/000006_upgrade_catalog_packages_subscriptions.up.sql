CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "btree_gist";

-- Recreate Catalog & Package & Subscription tables according to SPEC-BE-PLATFORM-PACKAGE-CATALOG v1.0
DROP TABLE IF EXISTS organization_subscriptions CASCADE;
DROP TABLE IF EXISTS service_package_features CASCADE;
DROP TABLE IF EXISTS service_package_versions CASCADE;
DROP TABLE IF EXISTS service_packages CASCADE;
DROP TABLE IF EXISTS menus CASCADE;
DROP TABLE IF EXISTS tenant_routes CASCADE;
DROP TABLE IF EXISTS permissions CASCADE;
DROP TABLE IF EXISTS features CASCADE;

-- 1. Danh mục Tính năng thương mại (Features)
CREATE TABLE features (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(80) NOT NULL,
    module VARCHAR(50) NOT NULL,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    is_core BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_features_code ON features (UPPER(code));

-- 2. Danh mục Quyền chuẩn của Tenant (Permission Catalog)
CREATE TABLE permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_id UUID NOT NULL REFERENCES features(id) ON DELETE RESTRICT,
    code VARCHAR(100) NOT NULL,
    resource VARCHAR(60) NOT NULL,
    action VARCHAR(40) NOT NULL,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    is_sensitive BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_permissions_code ON permissions (LOWER(code));
CREATE INDEX idx_permissions_feature ON permissions (feature_id);

-- 3. Route Registry cho Tenant Frontend (Đảm bảo menu chỉ trỏ tới route hợp lệ)
CREATE TABLE tenant_routes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_key VARCHAR(100) NOT NULL UNIQUE,
    path VARCHAR(255) NOT NULL UNIQUE,
    feature_id UUID REFERENCES features(id) ON DELETE RESTRICT,
    name VARCHAR(150) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 4. Cây Menu nền mặc định cho Tenant
CREATE TABLE menus (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id UUID REFERENCES menus(id) ON DELETE CASCADE,
    menu_key VARCHAR(100) NOT NULL UNIQUE,
    label VARCHAR(120) NOT NULL,
    icon VARCHAR(60),
    menu_type VARCHAR(20) NOT NULL DEFAULT 'ITEM' CHECK (menu_type IN ('GROUP', 'SUBMENU', 'ITEM')),
    route_id UUID REFERENCES tenant_routes(id) ON DELETE RESTRICT,
    feature_id UUID REFERENCES features(id) ON DELETE RESTRICT,
    required_permissions JSONB NOT NULL DEFAULT '[]'::jsonb,
    permission_match_mode VARCHAR(10) NOT NULL DEFAULT 'ANY' CHECK (permission_match_mode IN ('ANY', 'ALL')),
    target_portals JSONB NOT NULL DEFAULT '["SCHOOL_ADMIN"]'::jsonb,
    is_mandatory BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order INT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_menus_parent_sort ON menus (parent_id, sort_order);

-- 5. Định nghĩa Gói dịch vụ (Package Master)
CREATE TABLE service_packages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(60) NOT NULL,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    package_type VARCHAR(20) NOT NULL CHECK (package_type IN ('PRIMARY', 'ADDON')),
    is_public BOOLEAN NOT NULL DEFAULT TRUE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by UUID REFERENCES platform_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_service_packages_code ON service_packages (UPPER(code));

-- 6. Phiên bản Gói dịch vụ (Immutable sau khi PUBLISHED)
CREATE TABLE service_package_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    package_id UUID NOT NULL REFERENCES service_packages(id) ON DELETE RESTRICT,
    version_number INT NOT NULL,
    version_label VARCHAR(40) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'PUBLISHED', 'DEPRECATED')),
    billing_cycle VARCHAR(20) NOT NULL DEFAULT 'YEARLY' CHECK (billing_cycle IN ('MONTHLY', 'YEARLY', 'CUSTOM')),
    base_price NUMERIC(15, 2) NOT NULL DEFAULT 0,
    currency VARCHAR(10) NOT NULL DEFAULT 'VND',
    quotas JSONB NOT NULL DEFAULT '{}'::jsonb,
    release_notes TEXT,
    published_at TIMESTAMPTZ,
    published_by UUID REFERENCES platform_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (package_id, version_number)
);

-- 7. Danh sách Feature nằm trong một Phiên bản Gói
CREATE TABLE service_package_features (
    package_version_id UUID NOT NULL REFERENCES service_package_versions(id) ON DELETE CASCADE,
    feature_id UUID NOT NULL REFERENCES features(id) ON DELETE RESTRICT,
    feature_limits JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (package_version_id, feature_id)
);

-- 8. Đăng ký Gói dịch vụ của Organization (1 Gói chính + N Add-ons)
CREATE TABLE organization_subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    package_version_id UUID NOT NULL REFERENCES service_package_versions(id) ON DELETE RESTRICT,
    subscription_type VARCHAR(20) NOT NULL CHECK (subscription_type IN ('PRIMARY', 'ADDON')),
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('SCHEDULED', 'ACTIVE', 'SUSPENDED', 'EXPIRED', 'REVOKED')),
    started_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    effective_period TSTZRANGE GENERATED ALWAYS AS (tstzrange(started_at, expires_at, '[)')) STORED,
    custom_quotas JSONB,
    auto_renew BOOLEAN NOT NULL DEFAULT FALSE,
    notes TEXT,
    assigned_by UUID REFERENCES platform_users(id),
    revoked_at TIMESTAMPTZ,
    revoked_by UUID REFERENCES platform_users(id),
    revoke_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at > started_at)
);

-- Ràng buộc chống chồng lấn thời gian cho Gói chính (PRIMARY) đang hoạt động/lên lịch
ALTER TABLE organization_subscriptions
ADD CONSTRAINT excl_org_primary_subscription_overlap
EXCLUDE USING GIST (
    organization_id WITH =,
    effective_period WITH &&
)
WHERE (subscription_type = 'PRIMARY' AND status IN ('SCHEDULED', 'ACTIVE', 'SUSPENDED'));

CREATE INDEX idx_org_subs_org_status ON organization_subscriptions (organization_id, status, expires_at);

-- Bật RLS cho bảng organization_subscriptions theo nguyên tắc mục 4 của Master Doc
ALTER TABLE organization_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE organization_subscriptions FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_org_subscriptions ON organization_subscriptions
    USING (
        current_setting('app.bypass_rls', TRUE) = 'on'
        OR NULLIF(current_setting('app.organization_id', TRUE), '') IS NULL
        OR organization_id = NULLIF(current_setting('app.organization_id', TRUE), '')::UUID
    )
    WITH CHECK (
        current_setting('app.bypass_rls', TRUE) = 'on'
        OR NULLIF(current_setting('app.organization_id', TRUE), '') IS NULL
        OR organization_id = NULLIF(current_setting('app.organization_id', TRUE), '')::UUID
    );

-- ============================================================================
-- SEED DATA MẪU CHO CATALOG, ROUTES, MENUS & GÓI DỊCH VỤ (Checklist 6.2 mục 2)
-- ============================================================================

INSERT INTO features (id, code, module, name, description, is_core, is_active) VALUES
('550e8400-e29b-41d4-a716-446655440001', 'CORE_ACADEMIC', 'ACADEMIC', 'Quản lý Đào tạo & Lớp học', 'Cấp học, năm học, học kỳ, khối, lớp, môn học và thời khóa biểu', TRUE, TRUE),
('550e8400-e29b-41d4-a716-446655440002', 'STUDENT_PROFILE', 'STUDENT', 'Hồ sơ Học sinh & Phụ huynh', 'Quản lý hồ sơ học sinh, nhập học, người giám hộ và quan hệ gia đình', TRUE, TRUE),
('550e8400-e29b-41d4-a716-446655440003', 'HR_STAFF', 'HR', 'Nhân sự & Tổ chức trường', 'Cơ cấu phòng ban, hồ sơ giáo viên/nhân viên và phân công chuyên môn', TRUE, TRUE),
('550e8400-e29b-41d4-a716-446655440004', 'ATTENDANCE', 'ACADEMIC', 'Điểm danh & Chuyên cần', 'Điểm danh học sinh theo ngày/tiết và thống kê chuyên cần', FALSE, TRUE),
('550e8400-e29b-41d4-a716-446655440005', 'GRADEBOOK', 'ACADEMIC', 'Sổ điểm & Học bạ điện tử', 'Nhập điểm, tổng kết điểm và phê duyệt học bạ', FALSE, TRUE),
('550e8400-e29b-41d4-a716-446655440006', 'FINANCE_FEE', 'FINANCE', 'Tài chính & Học phí', 'Thiết lập biểu phí, đợt thu, gạch nợ học phí và hoàn phí', FALSE, TRUE),
('550e8400-e29b-41d4-a716-446655440007', 'LIBRARY', 'OPS', 'Thư viện trường học', 'Quản lý đầu sách, mượn trả và thẻ thư viện', FALSE, TRUE)
ON CONFLICT DO NOTHING;

INSERT INTO permissions (id, feature_id, code, resource, action, name, is_sensitive, is_active) VALUES
('660e8400-e29b-41d4-a716-446655440001', '550e8400-e29b-41d4-a716-446655440001', 'academic:read', 'academic', 'read', 'Xem thông tin lớp học & năm học', FALSE, TRUE),
('660e8400-e29b-41d4-a716-446655440002', '550e8400-e29b-41d4-a716-446655440001', 'academic:manage', 'academic', 'manage', 'Quản lý lớp học, môn học & phân công', FALSE, TRUE),
('660e8400-e29b-41d4-a716-446655440003', '550e8400-e29b-41d4-a716-446655440002', 'student:read', 'student', 'read', 'Xem hồ sơ học sinh', FALSE, TRUE),
('660e8400-e29b-41d4-a716-446655440004', '550e8400-e29b-41d4-a716-446655440002', 'student:create', 'student', 'create', 'Tiếp nhận và tạo mới hồ sơ học sinh', FALSE, TRUE),
('660e8400-e29b-41d4-a716-446655440005', '550e8400-e29b-41d4-a716-446655440003', 'hr:read', 'hr', 'read', 'Xem danh sách nhân sự trường', FALSE, TRUE),
('660e8400-e29b-41d4-a716-446655440006', '550e8400-e29b-41d4-a716-446655440004', 'attendance:read', 'attendance', 'read', 'Xem dữ liệu điểm danh', FALSE, TRUE),
('660e8400-e29b-41d4-a716-446655440007', '550e8400-e29b-41d4-a716-446655440004', 'attendance:input', 'attendance', 'input', 'Thực hiện điểm danh lớp học', FALSE, TRUE),
('660e8400-e29b-41d4-a716-446655440008', '550e8400-e29b-41d4-a716-446655440005', 'grade:input', 'grade', 'input', 'Nhập điểm môn học', FALSE, TRUE),
('660e8400-e29b-41d4-a716-446655440009', '550e8400-e29b-41d4-a716-446655440005', 'grade:delete', 'grade', 'delete', 'Xóa hoặc mở khóa bảng điểm đã chốt', TRUE, TRUE),
('660e8400-e29b-41d4-a716-446655440010', '550e8400-e29b-41d4-a716-446655440006', 'fee:collect', 'fee', 'collect', 'Thu học phí và xuất biên lai', FALSE, TRUE),
('660e8400-e29b-41d4-a716-446655440011', '550e8400-e29b-41d4-a716-446655440006', 'fee:refund', 'fee', 'refund', 'Hoàn trả học phí', TRUE, TRUE)
ON CONFLICT DO NOTHING;

INSERT INTO tenant_routes (id, route_key, path, feature_id, name, is_active) VALUES
('770e8400-e29b-41d4-a716-446655440001', 'tenant.dashboard', '/dashboard', NULL, 'Trang tổng quan trường học', TRUE),
('770e8400-e29b-41d4-a716-446655440002', 'academic.classes.list', '/academic/classes', '550e8400-e29b-41d4-a716-446655440001', 'Danh sách lớp học', TRUE),
('770e8400-e29b-41d4-a716-446655440003', 'student.profiles.list', '/students', '550e8400-e29b-41d4-a716-446655440002', 'Hồ sơ học sinh', TRUE),
('770e8400-e29b-41d4-a716-446655440004', 'hr.staff.list', '/hr/staff', '550e8400-e29b-41d4-a716-446655440003', 'Danh sách giáo viên & nhân viên', TRUE),
('770e8400-e29b-41d4-a716-446655440005', 'academic.attendance', '/academic/attendance', '550e8400-e29b-41d4-a716-446655440004', 'Điểm danh học sinh', TRUE),
('770e8400-e29b-41d4-a716-446655440006', 'academic.gradebook', '/academic/gradebook', '550e8400-e29b-41d4-a716-446655440005', 'Sổ điểm điện tử', TRUE),
('770e8400-e29b-41d4-a716-446655440007', 'finance.fees', '/finance/fees', '550e8400-e29b-41d4-a716-446655440006', 'Thu học phí & Công nợ', TRUE)
ON CONFLICT DO NOTHING;

INSERT INTO menus (id, parent_id, menu_key, label, icon, menu_type, route_id, feature_id, required_permissions, permission_match_mode, target_portals, is_mandatory, sort_order, is_active) VALUES
('880e8400-e29b-41d4-a716-446655440001', NULL, 'tenant-dashboard', 'Tổng quan', 'DashboardOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440001', NULL, '[]'::jsonb, 'ANY', '["SCHOOL_ADMIN","TEACHER","STAFF"]'::jsonb, TRUE, 1, TRUE),
('880e8400-e29b-41d4-a716-446655440002', NULL, 'tenant-academic', 'Đào tạo & Lớp học', 'ReadOutlined', 'SUBMENU', NULL, '550e8400-e29b-41d4-a716-446655440001', '[]'::jsonb, 'ANY', '["SCHOOL_ADMIN","TEACHER"]'::jsonb, FALSE, 2, TRUE),
('880e8400-e29b-41d4-a716-446655440003', '880e8400-e29b-41d4-a716-446655440002', 'tenant-classes', 'Danh sách Lớp học', 'TeamOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440002', '550e8400-e29b-41d4-a716-446655440001', '["academic:read"]'::jsonb, 'ANY', '["SCHOOL_ADMIN","TEACHER"]'::jsonb, FALSE, 1, TRUE),
('880e8400-e29b-41d4-a716-446655440004', '880e8400-e29b-41d4-a716-446655440002', 'tenant-attendance', 'Điểm danh', 'CheckSquareOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440005', '550e8400-e29b-41d4-a716-446655440004', '["attendance:read","attendance:input"]'::jsonb, 'ANY', '["SCHOOL_ADMIN","TEACHER"]'::jsonb, FALSE, 2, TRUE),
('880e8400-e29b-41d4-a716-446655440005', '880e8400-e29b-41d4-a716-446655440002', 'tenant-gradebook', 'Sổ điểm', 'ProfileOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440006', '550e8400-e29b-41d4-a716-446655440005', '["grade:input"]'::jsonb, 'ANY', '["SCHOOL_ADMIN","TEACHER"]'::jsonb, FALSE, 3, TRUE),
('880e8400-e29b-41d4-a716-446655440006', NULL, 'tenant-students', 'Hồ sơ Học sinh', 'UserOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440003', '550e8400-e29b-41d4-a716-446655440002', '["student:read"]'::jsonb, 'ANY', '["SCHOOL_ADMIN","TEACHER","STAFF"]'::jsonb, FALSE, 3, TRUE),
('880e8400-e29b-41d4-a716-446655440007', NULL, 'tenant-finance', 'Học phí & Tài chính', 'DollarOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440007', '550e8400-e29b-41d4-a716-446655440006', '["fee:collect"]'::jsonb, 'ANY', '["SCHOOL_ADMIN","STAFF"]'::jsonb, FALSE, 4, TRUE)
ON CONFLICT DO NOTHING;

-- Seed Gói dịch vụ mẫu (PKG_STANDARD + ADDON_FINANCE)
INSERT INTO service_packages (id, code, name, description, package_type, is_public, is_active) VALUES
('990e8400-e29b-41d4-a716-446655440001', 'PKG_STANDARD', 'Gói Tiêu Chuẩn', 'Gói nền tảng tiêu chuẩn dành cho trường phổ thông', 'PRIMARY', TRUE, TRUE),
('990e8400-e29b-41d4-a716-446655440002', 'ADDON_FINANCE', 'Phân hệ Tài chính & Học phí', 'Gói bổ sung quản lý thu học phí, công nợ và hoàn phí', 'ADDON', TRUE, TRUE)
ON CONFLICT DO NOTHING;

INSERT INTO service_package_versions (id, package_id, version_number, version_label, status, billing_cycle, base_price, currency, quotas, release_notes, published_at) VALUES
('770e8400-e29b-41d4-a716-446655440098', '990e8400-e29b-41d4-a716-446655440001', 1, 'v1.0 - Năm học 2026-2027', 'PUBLISHED', 'YEARLY', 12000000, 'VND', '{"max_schools": 1, "max_students": 1500, "max_staff": 150, "max_storage_gb": 50}'::jsonb, 'Phiên bản chuẩn khởi tạo', NOW()),
('770e8400-e29b-41d4-a716-446655440099', '990e8400-e29b-41d4-a716-446655440002', 1, 'v1.0 - Add-on Tài chính', 'PUBLISHED', 'YEARLY', 5000000, 'VND', '{"max_storage_gb": 20}'::jsonb, 'Phiên bản Add-on Tài chính', NOW())
ON CONFLICT DO NOTHING;

INSERT INTO service_package_features (package_version_id, feature_id) VALUES
('770e8400-e29b-41d4-a716-446655440098', '550e8400-e29b-41d4-a716-446655440001'),
('770e8400-e29b-41d4-a716-446655440098', '550e8400-e29b-41d4-a716-446655440002'),
('770e8400-e29b-41d4-a716-446655440098', '550e8400-e29b-41d4-a716-446655440003'),
('770e8400-e29b-41d4-a716-446655440098', '550e8400-e29b-41d4-a716-446655440004'),
('770e8400-e29b-41d4-a716-446655440098', '550e8400-e29b-41d4-a716-446655440005'),
('770e8400-e29b-41d4-a716-446655440099', '550e8400-e29b-41d4-a716-446655440006')
ON CONFLICT DO NOTHING;
