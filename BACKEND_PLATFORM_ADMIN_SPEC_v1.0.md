# TÀI LIỆU ĐẶC TẢ TRIỂN KHAI BACKEND — PLATFORM ADMIN (ADMIN NỀN TẢNG)

**Mã tài liệu:** `SPEC-BE-PLATFORM-ADMIN`  
**Phiên bản:** 1.0  
**Trạng thái:** Ready for Coding (Đồng bộ với `SCHOOL_ERP_PROJECT_MASTER_v1.3` và `PLATFORM_ADMIN_SPECIFICATION_v1.1`)  
**Tech Stack:** Go (Golang), Fiber v2, GORM, PostgreSQL 16+, Redis 7+  
**Domain triển khai:** `api.school-erp.vn` (Namespace `/api/v1/platform/*`)  

---

## 1. Tổng quan Kiến trúc Backend (Modular Monolith)

Phân hệ **Platform Admin Backend** được tổ chức như một Bounded Context độc lập bên trong dự án Go Modular Monolith, chịu trách nhiệm phục vụ cổng quản trị nền tảng (`admin.school-erp.vn` — Nuxt 4 + Ant Design Vue).

### 1.1 Nguyên tắc thiết kế cốt lõi
1. **Dual-Identity & Cross-Realm Isolation:**
   - Tách biệt hoàn toàn bảng dữ liệu (`platform_*`), luồng đăng nhập, Redis session và JWT claims (`realm: "platform"`) khỏi hệ thống Tenant (`users`).
   - Không bật PostgreSQL Row Level Security (RLS) cô lập theo `organization_id` trên các bảng `platform_*`.
   - Khi Platform Admin thao tác trên bảng dữ liệu của Tenant (ví dụ: tạo Organization, tạo School, tạo tài khoản School Admin đầu tiên, cấp Subscription), Service của Platform thực thi trong Transaction chuẩn có ghi nhận `audit_logs` với `actor_type = 'PLATFORM'`.
2. **Cache-Aside & Explicit Invalidation với Redis:**
   - Mọi truy vấn xác thực session, kiểm tra quyền (`platform_permissions`) và lấy cây menu (`platform_menus`) đều ưu tiên đọc từ Redis.
   - Mọi API ghi (Create/Update/Delete/Revoke) bắt buộc thực hiện xóa/cập nhật key Redis ngay sau khi commit DB Transaction thành công.
3. **Immutable Package Versioning:**
   - Gói dịch vụ (`service_packages`) sau khi `PUBLISH` một `service_package_version` thì phiên bản đó trở thành bất biến (read-only về cấu trúc Feature).

### 1.2 Cấu trúc thư mục module Platform trong Go

```text
internal/
├── platform/                       # Bounded Context: Platform Admin
│   ├── auth/                       # Login, Refresh, Logout, MFA, JWT Realm 'platform'
│   ├── iam/                        # Quản lý platform_users, platform_roles, platform_permissions
│   ├── menu/                       # Quản lý platform_menus & tree builder cho AntD Vue
│   ├── tenant_mgmt/                # Quản lý Organizations, Schools, Subdomains, Reserved Keywords
│   ├── catalog/                    # Quản lý Tenant Catalog (features, permissions, base menus)
│   ├── billing/                    # Quản lý Service Packages, Versions, Subscriptions, Add-ons
│   ├── support/                    # Quản lý Support Mode Sessions (Four-eyes approval & token exchange)
│   ├── audit/                      # Ghi và tra cứu Audit Logs (actor_type = PLATFORM)
│   └── router.go                   # Đăng ký toàn bộ routes /api/v1/platform/*
├── middleware/
│   ├── cors_dynamic.go             # Dynamic CORS cho admin.school-erp.vn & *.school-erp.vn
│   ├── platform_auth.go            # Xác thực JWT realm='platform' + check Redis session/blacklist
│   └── platform_perm.go            # Middleware RequirePlatformPermission("platform:...")
└── pkg/
    ├── rediskeys/                  # Định nghĩa hằng số Redis Key & TTL tập trung
    └── response/                   # Chuẩn hóa JSON Response & Error Model
```

---

## 2. Thiết kế Cơ sở dữ liệu PostgreSQL (DDL & Seed Data)

Sử dụng công cụ migration SQL thuần (`golang-migrate` hoặc `goose`). Không dùng `gorm.AutoMigrate` cho các bảng này.

### 2.1 Migration `000001_init_platform_iam_and_menus.up.sql`

```sql
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- 1. Bảng tài khoản Admin nền tảng (Tách biệt hoàn toàn khỏi bảng users của trường học)
CREATE TABLE platform_users (
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
CREATE UNIQUE INDEX uq_platform_users_email ON platform_users (LOWER(email)) WHERE deleted_at IS NULL;

-- 2. Bảng vai trò nội bộ nền tảng
CREATE TABLE platform_roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    is_system BOOLEAN NOT NULL DEFAULT FALSE, -- TRUE với ROOT_ADMIN, cấm xóa/sửa code
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_platform_roles_code ON platform_roles (UPPER(code));

-- 3. Bảng danh mục quyền hạn cổng Platform Admin
CREATE TABLE platform_permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(100) NOT NULL, -- Chuẩn: platform:<resource>:<action>
    module VARCHAR(50) NOT NULL, -- PLATFORM_IAM, ORGANIZATION, CATALOG, PACKAGE, SUPPORT, AUDIT
    name VARCHAR(150) NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_platform_permissions_code ON platform_permissions (LOWER(code));

-- 4. Bảng trung gian Role - Permission
CREATE TABLE platform_role_permissions (
    platform_role_id UUID NOT NULL REFERENCES platform_roles(id) ON DELETE CASCADE,
    platform_permission_id UUID NOT NULL REFERENCES platform_permissions(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (platform_role_id, platform_permission_id)
);

-- 5. Bảng trung gian User - Role
CREATE TABLE platform_user_roles (
    platform_user_id UUID NOT NULL REFERENCES platform_users(id) ON DELETE CASCADE,
    platform_role_id UUID NOT NULL REFERENCES platform_roles(id) ON DELETE CASCADE,
    assigned_by UUID REFERENCES platform_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (platform_user_id, platform_role_id)
);

-- 6. Cây Menu điều hướng cổng admin.school-erp.vn (Tối ưu cho Ant Design Vue <a-menu>)
CREATE TABLE platform_menus (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id UUID REFERENCES platform_menus(id) ON DELETE CASCADE,
    menu_key VARCHAR(100) NOT NULL UNIQUE, -- Ánh xạ thuộc tính `key` của AntD MenuItem
    label VARCHAR(120) NOT NULL,           -- Nhãn hiển thị
    icon VARCHAR(60),                      -- Tên component icon từ @ant-design/icons-vue
    route_path VARCHAR(255),               -- Nuxt 4 route path (NULL nếu là SUBMENU/GROUP)
    menu_type VARCHAR(20) NOT NULL DEFAULT 'ITEM' CHECK (menu_type IN ('GROUP', 'SUBMENU', 'ITEM')),
    required_permission_code VARCHAR(100), -- NULL nếu mọi user đăng nhập đều thấy
    badge_key VARCHAR(50),                 -- Key hiển thị badge đếm số lượng (VD: 'pending_orgs')
    sort_order INT NOT NULL DEFAULT 0,
    is_hidden BOOLEAN NOT NULL DEFAULT FALSE, -- TRUE cho route chi tiết cần map active menu cha
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_platform_menus_parent_sort ON platform_menus (parent_id, sort_order);
```

### 2.2 Migration `000002_init_platform_support_and_audit.up.sql`

```sql
-- 7. Bảng từ khóa subdomain cấm đăng ký
CREATE TABLE reserved_subdomains (
    keyword VARCHAR(40) PRIMARY KEY,
    reason VARCHAR(255) NOT NULL,
    created_by UUID REFERENCES platform_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 8. Bảng lịch sử đổi subdomain
CREATE TABLE organization_subdomain_histories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    old_subdomain VARCHAR(40) NOT NULL,
    new_subdomain VARCHAR(40) NOT NULL,
    approved_by UUID NOT NULL REFERENCES platform_users(id),
    reason TEXT NOT NULL,
    redirect_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 9. Bảng phiên hỗ trợ ủy quyền (Support Mode)
CREATE TABLE support_access_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_platform_user_id UUID NOT NULL REFERENCES platform_users(id),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    school_id UUID REFERENCES schools(id),
    session_type VARCHAR(20) NOT NULL CHECK (session_type IN ('READ_ONLY', 'READ_WRITE')),
    reason TEXT NOT NULL,
    granted_permissions JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'APPROVED', 'ACTIVE', 'EXPIRED', 'REVOKED', 'REJECTED')),
    approved_by UUID REFERENCES platform_users(id),
    started_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revoked_by UUID REFERENCES platform_users(id),
    revoke_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_support_sessions_actor ON support_access_sessions (actor_platform_user_id, status);
CREATE INDEX idx_support_sessions_org ON support_access_sessions (organization_id, status);

-- 10. Bảng Audit Logs dùng chung toàn hệ thống (Phân tách actor_type)
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_type VARCHAR(20) NOT NULL CHECK (actor_type IN ('PLATFORM', 'TENANT', 'SUPPORT_MODE', 'SYSTEM')),
    actor_id UUID,
    support_session_id UUID REFERENCES support_access_sessions(id),
    organization_id UUID,
    school_id UUID,
    action VARCHAR(100) NOT NULL,
    resource_type VARCHAR(60) NOT NULL,
    resource_id UUID,
    before_data JSONB,
    after_data JSONB,
    reason TEXT,
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_audit_logs_actor ON audit_logs (actor_type, actor_id, created_at DESC);
CREATE INDEX idx_audit_logs_org ON audit_logs (organization_id, created_at DESC);
```

### 2.3 Seed Data chuẩn (`seed_platform_core.sql`)

```sql
-- 1. Seed Vai trò hệ thống ROOT_ADMIN
INSERT INTO platform_roles (id, code, name, description, is_system)
VALUES ('00000000-0000-0000-0000-000000000001', 'ROOT_ADMIN', 'Quản trị viên Tối cao', 'Toàn quyền quản trị nền tảng SaaS', TRUE)
ON CONFLICT DO NOTHING;

-- 2. Seed Danh mục Quyền nền tảng
INSERT INTO platform_permissions (code, module, name) VALUES
('platform:user:read', 'PLATFORM_IAM', 'Xem danh sách nhân sự nền tảng'),
('platform:user:manage', 'PLATFORM_IAM', 'Quản lý tài khoản nhân sự nền tảng'),
('platform:role:manage', 'PLATFORM_IAM', 'Quản lý vai trò và phân quyền nền tảng'),
('platform:org:read', 'ORGANIZATION', 'Xem danh sách và chi tiết Tổ chức/Trường'),
('platform:org:create', 'ORGANIZATION', 'Khởi tạo Tổ chức mới'),
('platform:org:approve', 'ORGANIZATION', 'Phê duyệt Tổ chức đăng ký'),
('platform:org:status', 'ORGANIZATION', 'Khóa/Mở khóa Tổ chức'),
('platform:subdomain:manage', 'ORGANIZATION', 'Quản lý subdomain giữ chỗ và đổi subdomain'),
('platform:feature:manage', 'CATALOG', 'Quản lý danh mục tính năng (Features)'),
('platform:permission:manage', 'CATALOG', 'Quản lý danh mục quyền Tenant (Permissions)'),
('platform:menu:manage', 'CATALOG', 'Quản lý cây menu nền cho Tenant'),
('platform:package:read', 'PACKAGE', 'Xem danh sách gói dịch vụ và Add-on'),
('platform:package:manage', 'PACKAGE', 'Tạo và phát hành phiên bản gói dịch vụ'),
('platform:subscription:manage', 'PACKAGE', 'Cấp, gia hạn và thu hồi gói dịch vụ của Tổ chức'),
('platform:support:request', 'SUPPORT', 'Tạo yêu cầu phiên hỗ trợ vào trường'),
('platform:support:approve', 'SUPPORT', 'Phê duyệt phiên hỗ trợ'),
('platform:support:revoke', 'SUPPORT', 'Thu hồi phiên hỗ trợ đang hoạt động'),
('platform:audit:read', 'AUDIT', 'Tra cứu nhật ký kiểm toán hệ thống')
ON CONFLICT DO NOTHING;

-- 3. Gán toàn bộ quyền cho ROOT_ADMIN
INSERT INTO platform_role_permissions (platform_role_id, platform_permission_id)
SELECT '00000000-0000-0000-0000-000000000001', id FROM platform_permissions
ON CONFLICT DO NOTHING;

-- 4. Seed Từ khóa Subdomain cấm
INSERT INTO reserved_subdomains (keyword, reason) VALUES
('admin', 'Cổng quản trị nền tảng'),
('api', 'Cổng API tập trung'),
('app', 'Cổng đăng nhập chung'),
('www', 'Trang chủ hệ thống'),
('auth', 'Dịch vụ xác thực'),
('cdn', 'Mạng phân phối nội dung'),
('mail', 'Hệ thống email'),
('support', 'Cổng hỗ trợ khách hàng'),
('system', 'Từ khóa hệ thống')
ON CONFLICT DO NOTHING;
```

---

## 3. Chiến lược Redis Cache & Invalidation (Triển khai trong Go)

### 3.1 Định nghĩa hằng số Key & TTL (`internal/pkg/rediskeys/platform.go`)

```go
package rediskeys

import (
	"fmt"
	"time"
)

const (
	TTLPlatformSession     = 7 * 24 * time.Hour // Khớp Refresh Token
	TTLPlatformAuthz       = 15 * time.Minute
	TTLPlatformMenuTree    = 1 * time.Hour
	TTLTenantSubdomain     = 30 * time.Minute
	TTLTenantEntitlement   = 30 * time.Minute
)

func PlatformSessionKey(jti string) string {
	return fmt.Sprintf("platform:session:%s", jti)
}

func PlatformBlacklistJTIKey(jti string) string {
	return fmt.Sprintf("platform:blacklist:jti:%s", jti)
}

func PlatformUserAuthzKey(userID string) string {
	return fmt.Sprintf("platform:authz:user:%s", userID)
}

func PlatformMenuTreeKey() string {
	return "platform:menu:tree"
}

func TenantSubdomainKey(subdomain string) string {
	return fmt.Sprintf("tenant:subdomain:%s", subdomain)
}

func TenantEntitlementKey(orgID string) string {
	return fmt.Sprintf("tenant:entitlement:%s", orgID)
}

func SupportSessionKey(sessionID string) string {
	return fmt.Sprintf("support:session:%s", sessionID)
}
```

### 3.2 Quy tắc đọc/xóa Cache trong Service Go

1. **Xác thực Session (`PlatformAuthMiddleware`):**
   - Decode JWT, kiểm tra `claims.Realm == "platform"`.
   - Kiểm tra `EXISTS platform:blacklist:jti:{jti}` $\rightarrow$ Nếu `1`, trả về `401 Unauthorized` (`TOKEN_REVOKED`).
   - Kiểm tra `EXISTS platform:session:{jti}` $\rightarrow$ Nếu `0` (hết hạn hoặc đã logout), trả về `401 Unauthorized` (`SESSION_EXPIRED`).
2. **Kiểm tra Quyền (`RequirePlatformPermission(permCode)`):**
   - Kiểm tra `EXISTS platform:authz:user:{userID}`.
   - **Cache Hit:** Chạy lệnh `SISMEMBER platform:authz:user:{userID} {permCode}`. Nếu `false` $\rightarrow$ trả về `403 Forbidden`.
   - **Cache Miss:** Query `SELECT DISTINCT p.code FROM platform_permissions p JOIN platform_role_permissions rp ... WHERE ur.platform_user_id = ?`. Dùng Redis Pipeline lưu tập quyền vào `SET platform:authz:user:{userID}` kèm `EXPIRE 15m` (nếu user không có quyền nào, lưu placeholder `"__EMPTY__"` để chống Cache Penetration).
3. **Bảng tổng hợp Cache Invalidation:**

| Sự kiện Mutation trong Go Service | Lệnh Redis thực thi ngay sau Commit DB |
| :--- | :--- |
| Khóa nhân sự (`status = LOCKED`) | `DEL platform:session:{all_user_jtis}`, `SET platform:blacklist:jti:{active_jti} 1`, `DEL platform:authz:user:{user_id}` |
| Gán/gỡ Role cho nhân sự | `DEL platform:authz:user:{user_id}` |
| Cập nhật quyền của một Role | Query danh sách `user_id` có role đó $\rightarrow$ `DEL platform:authz:user:{user_id}` (Pipeline) |
| Cập nhật cây `platform_menus` | `DEL platform:menu:tree` |
| Duyệt / Khóa / Đổi subdomain Org | `DEL tenant:subdomain:{old_subdomain}`, `DEL tenant:subdomain:{new_subdomain}` |
| Cấp / Gia hạn / Thu hồi Subscription | `DEL tenant:entitlement:{org_id}` |
| Thu hồi phiên Support Mode | `DEL support:session:{session_id}` |

---

## 4. Middleware & Bảo mật API trên Go Fiber

### 4.1 Cấu hình Dynamic CORS (`internal/middleware/cors_dynamic.go`)
- Cho phép Origin `https://admin.school-erp.vn` và các subdomain hợp lệ khớp regex `^https://[a-z0-9]([a-z0-9-]{1,38}[a-z0-9])?\.school-erp\.vn$` (cùng `http://localhost:3000` khi `APP_ENV=development`).
- `AllowHeaders`: `"Origin, Content-Type, Accept, Authorization, X-Tenant-Subdomain, X-Request-ID"`.
- `AllowCredentials`: `true`.

### 4.2 Cấu trúc JWT Claims của Platform Admin
```go
type PlatformJWTClaims struct {
	UserID string `json:"sub"`
	Realm  string `json:"realm"` // Bắt buộc phải là "platform"
	JTI    string `json:"jti"`   // Session ID lưu trên Redis
	jwt.RegisteredClaims
}
```
*Lưu ý bảo mật:* Tuyệt đối dùng Secret Key / RSA Keypair riêng biệt cho `platform` JWT hoặc kiểm tra nghiêm ngặt `claims.Realm == "platform"` tại bước đầu tiên của `PlatformAuthMiddleware` để ngăn token của Tenant (`realm: "tenant"`) xâm nhập.

---

## 5. Đặc tả Chi tiết Các Nhóm API (`/api/v1/platform/*`)

### 5.1 Chuẩn Envelope Response & Error Model
Mọi API trả về cấu trúc thống nhất:
```json
// Thành công
{
  "success": true,
  "data": { ... },
  "meta": { "page": 1, "limit": 20, "total": 100 }
}

// Lỗi
{
  "success": false,
  "error": {
    "code": "PLATFORM_PERMISSION_DENIED",
    "message": "Bạn không có quyền thực hiện thao tác này",
    "details": null
  }
}
```

### 5.2 Nhóm 1: Auth & Profile (`/api/v1/platform/auth` & `/me`)

| Method | Endpoint | Permission | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/platform/auth/login` | Public | Validate email/password (`platform_users`), check `status == 'ACTIVE'`, verify TOTP nếu `mfa_enabled=true`. Tạo `jti`, lưu Redis `platform:session:{jti}`, cập nhật `last_login_at`. Trả về `access_token` (15m) + `refresh_token` (7d). |
| `POST` | `/api/v1/platform/auth/refresh` | Public | Đọc `refresh_token`, verify `platform:session:{jti}` tồn tại trên Redis, cấp `access_token` mới. |
| `POST` | `/api/v1/platform/auth/logout` | Authenticated | `DEL platform:session:{jti}` và `SET platform:blacklist:jti:{jti} 1 EX {remaining_ttl}`. |
| `GET` | `/api/v1/platform/me` | Authenticated | Trả về thông tin user, danh sách `roles`, danh sách `permissions` (từ Redis), và cây `menus` đã lọc tương thích trực tiếp với `<a-menu>` của Ant Design Vue. |

**Cấu trúc JSON trả về của `GET /api/v1/platform/me` (Chuẩn Nuxt 4 + AntD Vue):**
```json
{
  "success": true,
  "data": {
    "user": {
      "id": "uuid",
      "email": "admin@school-erp.vn",
      "full_name": "Super Admin",
      "avatar_url": null
    },
    "roles": ["ROOT_ADMIN"],
    "permissions": ["platform:org:read", "platform:package:manage"],
    "menus": [
      {
        "key": "dashboard",
        "label": "Tổng quan",
        "icon": "DashboardOutlined",
        "path": "/dashboard",
        "type": "ITEM",
        "hidden": false
      },
      {
        "key": "tenant-management",
        "label": "Khách hàng & Tổ chức",
        "icon": "BankOutlined",
        "type": "SUBMENU",
        "hidden": false,
        "children": [
          {
            "key": "org-list",
            "label": "Danh sách Tổ chức",
            "icon": "ApartmentOutlined",
            "path": "/organizations",
            "type": "ITEM",
            "badge_key": "pending_orgs",
            "hidden": false
          }
        ]
      }
    ]
  }
}
```
*Quy tắc build cây menu ở Backend:* Đọc từ `platform:menu:tree`. Lọc đệ quy từ dưới lên: với node `ITEM`, giữ lại nếu `required_permission_code == ""` hoặc nằm trong `userPermissions`. Với node `SUBMENU`/`GROUP`, chỉ giữ lại nếu có ít nhất 1 `children` sau khi lọc.

### 5.3 Nhóm 2: Quản trị Nhân sự & Phân quyền Platform (`/api/v1/platform/users`, `/roles`)

| Method | Endpoint | Permission | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/platform/users` | `platform:user:read` | Phân trang, tìm kiếm theo email/tên, lọc theo `status` và `role_id`. |
| `POST` | `/api/v1/platform/users` | `platform:user:manage` | Tạo user mới trong `platform_users`, hash password bằng Argon2id/Bcrypt, gán vào `platform_user_roles`. Ghi Audit Log. |
| `PUT` | `/api/v1/platform/users/:id/roles` | `platform:user:manage` | Thay thế danh sách vai trò của user. **Guard:** Cấm gỡ `ROOT_ADMIN` khỏi user cuối cùng giữ role này. Xóa cache `platform:authz:user:{id}`. |
| `PATCH` | `/api/v1/platform/users/:id/status` | `platform:user:manage` | Đổi trạng thái `ACTIVE` / `LOCKED`. **Guard:** Không cho tự khóa chính mình hoặc khóa `ROOT_ADMIN` cuối cùng. Nếu `LOCKED`, xóa toàn bộ session trên Redis. |
| `GET` | `/api/v1/platform/roles` | `platform:user:read` | Danh sách `platform_roles` kèm số lượng user và danh sách permission codes. |
| `POST` | `/api/v1/platform/roles` | `platform:role:manage` | Tạo vai trò mới (`is_system = false`). |
| `PUT` | `/api/v1/platform/roles/:id/permissions` | `platform:role:manage` | Cập nhật `platform_role_permissions` trong transaction. Xóa cache `platform:authz:user:*` của tất cả user thuộc role này. |

### 5.4 Nhóm 3: Quản trị Organization & Subdomain (`/api/v1/platform/organizations`)

| Method | Endpoint | Permission | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/platform/organizations` | `platform:org:read` | Danh sách Org kèm trạng thái, gói chính hiện tại, ngày hết hạn, tổng số trường. |
| `GET` | `/api/v1/platform/organizations/:id` | `platform:org:read` | Chi tiết Org, danh sách `schools`, danh sách `subscriptions` (gói chính + add-on) và lịch sử đổi subdomain. |
| `POST` | `/api/v1/platform/organizations` | `platform:org:create` | **Atomic Transaction:** Validate subdomain (regex + check `reserved_subdomains` + check unique `organizations`), tạo `organization`, tạo `school` đầu tiên, tạo tài khoản School Admin đầu tiên trong bảng `users` và `school_memberships`. Ghi Audit Log. |
| `POST` | `/api/v1/platform/organizations/:id/approve` | `platform:org:approve` | Chuyển trạng thái Org từ `PENDING_APPROVAL` sang `ACTIVE`. Xóa cache `tenant:subdomain:{subdomain}`. |
| `PATCH` | `/api/v1/platform/organizations/:id/status` | `platform:org:status` | Đổi trạng thái `ACTIVE` / `SUSPENDED` (bắt buộc nhập `reason`). Xóa cache `tenant:subdomain:{subdomain}`. |
| `POST` | `/api/v1/platform/organizations/:id/change-subdomain` | `platform:subdomain:manage` | Đổi subdomain mới (validate trùng & reserved), lưu bản ghi vào `organization_subdomain_histories`, xóa cache Redis của cả subdomain cũ và mới. |
| `GET` / `POST` | `/api/v1/platform/reserved-subdomains` | `platform:subdomain:manage` | Xem và thêm từ khóa subdomain cấm. |

### 5.5 Nhóm 4: Quản trị Catalog & Gói dịch vụ (`/catalog`, `/packages`, `/subscriptions`)

| Method | Endpoint | Permission | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `GET` / `POST` | `/api/v1/platform/catalog/features` | `platform:feature:manage` | Quản lý danh mục tính năng gốc của hệ thống (`code`, `name`, `module`, `is_active`). |
| `GET` / `POST` | `/api/v1/platform/catalog/permissions` | `platform:permission:manage` | Quản lý Permission Catalog cho Tenant (`code` chuẩn `resource:action`, gắn với `feature_id`). |
| `GET` / `PUT` | `/api/v1/platform/catalog/menus/tree` | `platform:menu:manage` | Lấy và cập nhật toàn bộ cây menu nền mặc định cho Tenant. |
| `GET` / `POST` | `/api/v1/platform/packages` | `platform:package:manage` | Tạo gói dịch vụ (`package_type`: `PRIMARY` hoặc `ADDON`). |
| `POST` | `/api/v1/platform/packages/:id/versions` | `platform:package:manage` | Tạo bản nháp `DRAFT` (`version = max_version + 1`), gán danh sách `feature_ids` và giới hạn định mức (quota JSONB). |
| `POST` | `/api/v1/platform/package-versions/:id/publish` | `platform:package:manage` | Chuyển trạng thái sang `PUBLISHED`. Sau bước này API từ chối mọi thao tác sửa đổi trên version này. |
| `POST` | `/api/v1/platform/organizations/:org_id/subscriptions` | `platform:subscription:manage` | Cấp gói chính hoặc Add-on (`package_version_id`, `started_at`, `expires_at`). Kiểm tra nếu là `PRIMARY` thì không được chồng lấn thời gian với gói chính đang `ACTIVE` khác. Xóa cache `tenant:entitlement:{org_id}`. |
| `PATCH` | `/api/v1/platform/subscriptions/:id/revoke` | `platform:subscription:manage` | Thu hồi gói dịch vụ trước hạn (bắt buộc `reason`). Xóa ngay cache `tenant:entitlement:{org_id}`. |

### 5.6 Nhóm 5: Support Mode (`/api/v1/platform/support-sessions`)

| Method | Endpoint | Permission | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/platform/support-sessions` | `platform:support:request` | Danh sách phiên hỗ trợ (lọc theo `status`, `organization_id`). |
| `POST` | `/api/v1/platform/support-sessions` | `platform:support:request` | Tạo yêu cầu hỗ trợ (`organization_id`, `school_id`, `session_type`, `reason`, `duration_minutes`). Nếu `READ_ONLY`, tự động chuyển `APPROVED`; nếu `READ_WRITE`, giữ `PENDING`. |
| `POST` | `/api/v1/platform/support-sessions/:id/approve` | `platform:support:approve` | Phê duyệt phiên hỗ trợ. **Four-Eyes Guard:** Từ chối nếu `session.actor_platform_user_id == currentUser.ID`. |
| `POST` | `/api/v1/platform/support-sessions/:id/exchange-token` | `platform:support:request` | Chỉ chủ nhân phiên hỗ trợ (`actor_platform_user_id == currentUser.ID`) mới được gọi khi status là `APPROVED`. Sinh JWT `realm: "support"`, lưu key `support:session:{id}` lên Redis với TTL = `expires_at - now()`. |
| `POST` | `/api/v1/platform/support-sessions/:id/revoke` | `platform:support:revoke` | Cập nhật DB `status = 'REVOKED'` và chạy `DEL support:session:{id}` trên Redis ngay lập tức. |

---

## 6. Quy chuẩn Ghi Audit Log & Xử lý Lỗi

### 6.1 Helper Ghi Audit Log bắt buộc
Mọi API Mutation (Create, Update, Delete, Approve, Suspend, Revoke, Publish) đều phải gọi `auditService.Log(tx, AuditEntry{...})` **bên trong cùng DB Transaction** với thao tác nghiệp vụ chính:
- `ActorType`: `"PLATFORM"`
- `ActorID`: `c.Locals("platform_user_id")`
- `Action`: Tên hành động viết hoa rõ nghĩa (VD: `"PLATFORM_ORG_CREATED"`, `"SUBSCRIPTION_REVOKED"`, `"SUPPORT_SESSION_APPROVED"`).
- `BeforeData` & `AfterData`: Snapshot JSON của bản ghi trước và sau khi thay đổi (ẩn toàn bộ trường nhạy cảm như `password_hash`, `mfa_secret`).
- `IPAddress` & `UserAgent`: Trích xuất từ Fiber Context (`c.IP()`, `c.Get("User-Agent")`).

### 6.2 Danh mục Mã lỗi chuẩn (Error Codes)

| HTTP Status | Error Code | Ý nghĩa |
| :--- | :--- | :--- |
| `401` | `INVALID_TOKEN_REALM` | Dùng token của Tenant để gọi vào API Platform |
| `401` | `SESSION_EXPIRED_OR_REVOKED` | Session không còn tồn tại trên Redis hoặc nằm trong blacklist |
| `403` | `PLATFORM_PERMISSION_DENIED` | Thiếu mã `platform_permissions` yêu cầu |
| `403` | `SELF_APPROVAL_NOT_ALLOWED` | Vi phạm nguyên tắc Four-Eyes khi tự duyệt phiên Support Mode của chính mình |
| `409` | `SUBDOMAIN_RESERVED_OR_TAKEN` | Subdomain nằm trong `reserved_subdomains` hoặc đã có Org sử dụng |
| `409` | `PACKAGE_VERSION_IMMUTABLE` | Cố tình chỉnh sửa `service_package_version` đã ở trạng thái `PUBLISHED` |
| `422` | `LAST_ROOT_ADMIN_PROTECTED` | Cố tình khóa hoặc gỡ quyền của `ROOT_ADMIN` cuối cùng trên hệ thống |

---

## 7. Checklist Triển khai Code cho Backend Developer

1. [ ] **Setup Migration:** Chạy `000001_init_platform_iam_and_menus.up.sql`, `000002_init_platform_support_and_audit.up.sql` và nạp `seed_platform_core.sql`. Tạo tài khoản `ROOT_ADMIN` đầu tiên qua CLI command (`go run cmd/cli/main.go create-root-admin`).
2. [ ] **Setup Redis Client & Keys:** Khởi tạo `go-redis/v9` và gói `internal/pkg/rediskeys`.
3. [ ] **Viết Middlewares:** Hoàn thiện `DynamicCORS`, `PlatformAuthMiddleware` (kiểm tra JWT `realm == "platform"` + Redis session/blacklist) và `RequirePlatformPermission`.
4. [ ] **Implement Auth & Menu Tree:** Hoàn thiện `/auth/login`, `/auth/refresh`, `/auth/logout` và `/me` (trả về cây menu chuẩn Ant Design Vue có cache Redis).
5. [ ] **Implement Platform IAM:** Hoàn thiện CRUD `platform_users`, `platform_roles` kèm logic xóa cache Redis khi phân quyền.
6. [ ] **Implement Organization & Package Management:** Hoàn thiện tạo Org (atomic transaction với School & School Admin đầu tiên), quản lý Package Versioning bất biến và cấp/thu hồi Subscription.
7. [ ] **Implement Support Mode:** Hoàn thiện luồng tạo yêu cầu, duyệt chéo (Four-Eyes) và đổi token tạm thời.
8. [ ] **Viết Integration Tests:** Kiểm thử bắt buộc 4 kịch bản: Cross-Realm Token Rejection, Redis Cache Invalidation khi thu hồi quyền/khóa user, Immutable Package Version, và Four-Eyes Support Approval.