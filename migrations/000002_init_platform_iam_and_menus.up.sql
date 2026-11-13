CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- 1. Bảng tài khoản Admin nền tảng (Tách biệt hoàn toàn khỏi bảng users của trường học)
CREATE TABLE IF NOT EXISTS platform_users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL,
    password_hash TEXT NOT NULL,
    full_name VARCHAR(150) NOT NULL,
    phone VARCHAR(20),
    avatar_url TEXT,
    mfa_secret TEXT,
    mfa_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'LOCKED', 'INACTIVE')),
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_platform_users_email ON platform_users (LOWER(email)) WHERE deleted_at IS NULL;

-- 2. Bảng vai trò nội bộ nền tảng
CREATE TABLE IF NOT EXISTS platform_roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_platform_roles_code ON platform_roles (UPPER(code));

-- 3. Bảng danh mục quyền hạn cổng Platform Admin
CREATE TABLE IF NOT EXISTS platform_permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(100) NOT NULL,
    module VARCHAR(50) NOT NULL,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_platform_permissions_code ON platform_permissions (LOWER(code));

-- 4. Bảng trung gian Role - Permission
CREATE TABLE IF NOT EXISTS platform_role_permissions (
    platform_role_id UUID NOT NULL REFERENCES platform_roles(id) ON DELETE CASCADE,
    platform_permission_id UUID NOT NULL REFERENCES platform_permissions(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (platform_role_id, platform_permission_id)
);

-- 5. Bảng trung gian User - Role
CREATE TABLE IF NOT EXISTS platform_user_roles (
    platform_user_id UUID NOT NULL REFERENCES platform_users(id) ON DELETE CASCADE,
    platform_role_id UUID NOT NULL REFERENCES platform_roles(id) ON DELETE CASCADE,
    assigned_by UUID REFERENCES platform_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (platform_user_id, platform_role_id)
);

-- 6. Cây Menu điều hướng cổng admin.school-erp.vn (Tối ưu cho Ant Design Vue <a-menu>)
CREATE TABLE IF NOT EXISTS platform_menus (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id UUID REFERENCES platform_menus(id) ON DELETE CASCADE,
    menu_key VARCHAR(100) NOT NULL UNIQUE,
    label VARCHAR(120) NOT NULL,
    icon VARCHAR(60),
    route_path VARCHAR(255),
    menu_type VARCHAR(20) NOT NULL DEFAULT 'ITEM' CHECK (menu_type IN ('GROUP', 'SUBMENU', 'ITEM')),
    required_permission_code VARCHAR(100),
    badge_key VARCHAR(50),
    sort_order INT NOT NULL DEFAULT 0,
    is_hidden BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_platform_menus_parent_sort ON platform_menus (parent_id, sort_order);
