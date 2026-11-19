# TÀI LIỆU ĐẶC TẢ TRIỂN KHAI BACKEND — LÕI ĐƠN VỊ / TỔ CHỨC & TRƯỜNG HỌC (TENANT CORE)

**Mã tài liệu:** `SPEC-BE-TENANT-CORE`  
**Phiên bản:** 1.0  
**Trạng thái:** Ready for Coding (Đồng bộ với `SCHOOL_ERP_PROJECT_MASTER_v1.3` — Gói PK01, PK02, PK03, PK04, PK09)  
**Tech Stack:** Go (Golang), Fiber v2, GORM, PostgreSQL 16+, Redis 7+  
**Domain triển khai:** `api.school-erp.vn` (Namespace `/api/v1/*`, phục vụ `{subdomain}.school-erp.vn`)

---

## 1. Kiến trúc Lõi Đa Đơn vị (Multi-Tenant Core Architecture)

Sau khi hoàn thiện phân hệ **Platform Admin**, đây là tài liệu hướng dẫn xây dựng nền móng cho phía **Đơn vị / Tổ chức (`Organization`) và Trường học (`School`)**.

### 1.1 Các nguyên tắc bảo mật & dữ liệu bắt buộc

1. **Cách ly dữ liệu 3 lớp (Defense-in-Depth):**
   * **Lớp 1 (API Middleware):** Xác minh chéo `X-Tenant-Subdomain` + JWT Token (`realm: "tenant"` hoặc `"support"`) + Membership hợp lệ trên Redis/DB.
   * **Lớp 2 (Composite Foreign Keys):** Mọi bảng con thuộc trường đều mang `(organization_id, school_id)` và khóa ngoại kép trỏ về bảng cha để PostgreSQL chặn đứng việc gắn nhầm dữ liệu giữa 2 đơn vị.
   * **Lớp 3 (PostgreSQL Row Level Security - RLS):** Bật `FORCE ROW LEVEL SECURITY` trên toàn bộ bảng nghiệp vụ của đơn vị. Mọi query GORM bắt buộc chạy qua transaction có thiết lập `SET LOCAL app.organization_id`.
2. **Chuỗi kiểm tra quyền hạn 3 tầng (Authorization Pipeline):**
   $$\text{Subscription Entitlement (Gói dịch vụ)} \longrightarrow \text{Role Permission (RBAC)} \longrightarrow \text{Data Scope (Phạm vi dữ liệu)}$$
3. **Không cấp quyền trực tiếp cho User trong MVP:** Quyền hiệu lực của một tài khoản tại trường là hợp nhất các `permissions` từ một hoặc nhiều `roles` được gán qua `school_memberships`, sau đó giao với danh sách `enabled_features` của `Organization`.

### 1.2 Cấu trúc Thư mục Module Tenant Core trong Go

```text
internal/
├── tenant/                         # Bounded Context: Hệ thống Đơn vị / Trường học
│   ├── auth/                       # Login Tenant, Switch School, Refresh, Support Token verify
│   ├── org_school/                 # Thông tin Organization, quản lý danh sách Schools trực thuộc
│   ├── iam/                        # Quản lý Users, Memberships, Custom Roles, User-Roles
│   ├── menu/                       # Tùy chỉnh Menu đơn vị & Render cây Menu động theo quyền
│   └── router.go                   # Đăng ký toàn bộ routes /api/v1/* (ngoại trừ /platform/*)
├── middleware/
│   ├── tenant_resolver.go          # Xác định & xác minh organization_id, school_id
│   └── tenant_authz.go             # Kiểm tra chuỗi Entitlement -> RBAC -> Data Scope
└── pkg/
    └── database/
        └── tenant_tx.go            # Wrapper WithTenantTx thực thi SET LOCAL cho PostgreSQL RLS
```

---

## 2. Thiết kế Cơ sở dữ liệu PostgreSQL & RLS (`000006_init_tenant_core_and_rls.up.sql`)

**Lưu ý hạ tầng DB:** Đảm bảo user kết nối PostgreSQL của ứng dụng Go (ví dụ `school_erp_app`) **không** có quyền `SUPERUSER` và **không** có thuộc tính `BYPASSRLS`.

```sql
-- 1. Bảng Đơn vị / Tổ chức (Global table quản lý bởi Platform, Tenant chỉ đọc thông tin của chính mình)
CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(50) NOT NULL UNIQUE,
    name VARCHAR(200) NOT NULL,
    subdomain VARCHAR(40) NOT NULL,
    logo_url TEXT,
    contact_email VARCHAR(255),
    contact_phone VARCHAR(20),
    address TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING_APPROVAL'
        CHECK (status IN ('PENDING_APPROVAL', 'ACTIVE', 'SUSPENDED', 'ARCHIVED')),
    settings JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_organizations_subdomain ON organizations (LOWER(subdomain)) WHERE deleted_at IS NULL;

-- 2. Bảng Trường học trực thuộc Đơn vị (Tenant-scoped)
CREATE TABLE schools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(200) NOT NULL,
    education_levels JSONB NOT NULL DEFAULT '[]'::jsonb, -- VD: ["PRIMARY", "SECONDARY", "HIGH_SCHOOL"]
    phone VARCHAR(20),
    email VARCHAR(255),
    address TEXT,
    logo_url TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (organization_id, id), -- Phục vụ Composite Foreign Key
    UNIQUE (organization_id, code)
);
CREATE INDEX idx_schools_org ON schools (organization_id);

-- 3. Bảng Danh tính Người dùng Tenant (Single Account Identity cho toàn bộ khách hàng)
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255),
    username VARCHAR(100),
    phone VARCHAR(20),
    password_hash TEXT NOT NULL,
    full_name VARCHAR(150) NOT NULL,
    avatar_url TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'LOCKED', 'INACTIVE')),
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_users_email ON users (LOWER(email)) WHERE email IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX uq_users_username ON users (LOWER(username)) WHERE username IS NOT NULL AND deleted_at IS NULL;

-- 4. Bảng Thành viên Trường học (Liên kết User <-> Organization <-> School)
CREATE TABLE school_memberships (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL,
    school_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_type VARCHAR(30) NOT NULL CHECK (member_type IN ('SCHOOL_ADMIN', 'TEACHER', 'STAFF', 'STUDENT', 'GUARDIAN')),
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED', 'INACTIVE')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (organization_id, school_id) REFERENCES schools(organization_id, id) ON DELETE CASCADE,
    UNIQUE (school_id, user_id),
    UNIQUE (organization_id, id)
);
CREATE INDEX idx_memberships_user ON school_memberships (user_id, status);
CREATE INDEX idx_memberships_org_school ON school_memberships (organization_id, school_id);

-- 5. Bảng Vai trò của Đơn vị / Trường (Gồm Role hệ thống seed sẵn & Role tùy chỉnh)
CREATE TABLE roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    school_id UUID, -- NULL nếu là Role dùng chung toàn Organization; có giá trị nếu chỉ áp dụng riêng 1 School
    code VARCHAR(60) NOT NULL,
    name VARCHAR(120) NOT NULL,
    description TEXT,
    data_scope VARCHAR(30) NOT NULL DEFAULT 'SCHOOL'
        CHECK (data_scope IN ('ORGANIZATION', 'SCHOOL', 'DEPARTMENT', 'ASSIGNED_CLASS', 'SELF')),
    is_system BOOLEAN NOT NULL DEFAULT FALSE, -- TRUE với SCHOOL_ADMIN mặc định
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (organization_id, school_id) REFERENCES schools(organization_id, id) ON DELETE CASCADE,
    UNIQUE (organization_id, id)
);
CREATE UNIQUE INDEX uq_roles_org_school_code ON roles (organization_id, COALESCE(school_id, '00000000-0000-0000-0000-000000000000'::uuid), UPPER(code));

-- 6. Bảng Gán Permission từ Catalog vào Role của Đơn vị
CREATE TABLE role_permissions (
    organization_id UUID NOT NULL,
    role_id UUID NOT NULL,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (role_id, permission_id),
    FOREIGN KEY (organization_id, role_id) REFERENCES roles(organization_id, id) ON DELETE CASCADE
);

-- 7. Bảng Gán Role cho Thành viên (1 Membership có thể giữ nhiều Role)
CREATE TABLE user_roles (
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
CREATE TABLE organization_menu_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    base_menu_id UUID REFERENCES menus(id) ON DELETE CASCADE, -- NULL nếu là menu riêng do Org tự tạo
    parent_config_id UUID REFERENCES organization_menu_configs(id) ON DELETE CASCADE,
    custom_label VARCHAR(120),
    custom_icon VARCHAR(60),
    custom_route_id UUID REFERENCES tenant_routes(id) ON DELETE RESTRICT, -- Chỉ dùng khi base_menu_id IS NULL
    sort_order INT NOT NULL DEFAULT 0,
    is_visible BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, base_menu_id)
);

-- ============================================================================
-- KÍCH HOẠT POSTGRESQL ROW LEVEL SECURITY (RLS) CHO TOÀN BỘ BẢNG TENANT-SCOPED
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
        EXECUTE format('
            CREATE POLICY tenant_isolation_%I ON %I
            USING (organization_id = NULLIF(current_setting(''app.organization_id'', TRUE), '''')::UUID)
            WITH CHECK (organization_id = NULLIF(current_setting(''app.organization_id'', TRUE), '''')::UUID);
        ', tbl, tbl);
    END LOOP;
END $$;
```

---

## 3. Cốt lõi Hạ tầng Go: Transaction RLS & Middlewares

### 3.1 Hàm bọc Transaction GORM thiết lập `SET LOCAL` (`internal/pkg/database/tenant_tx.go`)

Mọi truy vấn đọc/ghi vào các bảng có bật RLS bắt buộc phải đi qua hàm `WithTenantTx` dưới đây để đảm bảo connection pool không bị rò rỉ context giữa các request:

```go
package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type TenantContext struct {
	OrganizationID string
	SchoolID       string
}

// WithTenantTx mở một transaction và thiết lập SET LOCAL cho PostgreSQL RLS
func WithTenantTx(ctx context.Context, db *gorm.DB, tc TenantContext, fn func(tx *gorm.DB) error) error {
	if tc.OrganizationID == "" {
		return errors.New("MISSING_TENANT_ORGANIZATION_CONTEXT")
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SET LOCAL chỉ có hiệu lực trong phạm vi transaction hiện tại và tự hủy khi COMMIT/ROLLBACK
		if err := tx.Exec("SELECT set_config('app.organization_id', ?, true)", tc.OrganizationID).Error; err != nil {
			return fmt.Errorf("failed to set local organization_id: %w", err)
		}
		if tc.SchoolID != "" {
			if err := tx.Exec("SELECT set_config('app.school_id', ?, true)", tc.SchoolID).Error; err != nil {
				return fmt.Errorf("failed to set local school_id: %w", err)
			}
		}
		return fn(tx)
	})
}
```

### 3.2 Middleware Xác minh Ngữ cảnh Đơn vị (`internal/middleware/tenant_resolver.go`)

Luồng xử lý khi Frontend tại `{subdomain}.school-erp.vn` gọi API sang `api.school-erp.vn`:

1. **Giải mã JWT Token:** Kiểm tra `claims.Realm == "tenant"` (hoặc `"support"` nếu Platform Admin đang vào hỗ trợ).
2. **Đọc & Xác minh Subdomain:**
   * Đọc header `X-Tenant-Subdomain` từ request.
   * Tra cứu Redis `tenant:subdomain:{subdomain}` (nếu miss thì query bảng `organizations` và cache 30 phút) để lấy `organization_id` và kiểm tra `organizations.status == 'ACTIVE'`.
3. **Đối chiếu chéo Membership (Chống giả mạo header):**
   * **Nếu `realm == "tenant"`:** Kiểm tra trong Redis `tenant:user:orgs:{user_id}` (hoặc bảng `school_memberships`) xem `user_id` có thuộc `organization_id` này không. Nếu không $\rightarrow$ trả về `403 TENANT_ACCESS_DENIED`.
   * **Nếu `realm == "support"`:** Kiểm tra Redis `support:session:{session_id}` xem phiên hỗ trợ còn hạn không, `organization_id` có khớp không, và nếu `session_type == "READ_ONLY"` thì chặn toàn bộ method khác `GET/HEAD/OPTIONS` (`403 SUPPORT_READ_ONLY`).
4. **Xác định `school_id` hiện tại:** Đọc từ header `X-School-ID` (hoặc lấy trường mặc định đầu tiên của user trong Org đó), xác minh `school_id` thuộc `organization_id` và user có membership active tại trường đó. Lưu `TenantContext` vào `c.Locals("tenant_ctx")`.

### 3.3 Middleware Phân quyền 3 Lớp (`internal/middleware/tenant_authz.go`)

Sử dụng cú pháp gắn trên từng route nghiệp vụ, ví dụ: `RequireTenantAccess("ATTENDANCE", "attendance:read")`:

1. **Kiểm tra Lớp 1 — Feature Entitlement:**
   * Đọc `OrganizationEntitlement` từ Redis `tenant:entitlement:{org_id}` (tính từ gói chính + Add-ons).
   * Nếu `featureCode != ""` và không nằm trong `EnabledFeatures` $\rightarrow$ Trả về `403 FEATURE_NOT_SUBSCRIBED` (*"Đơn vị chưa đăng ký gói tính năng này"*).
2. **Kiểm tra Lớp 2 — Permission (RBAC):**
   * Đọc tập quyền hiệu lực của user tại trường hiện tại từ Redis `tenant:authz:member:{membership_id}` (hợp nhất từ tất cả `roles` mà `membership_id` đang giữ, **chỉ giữ lại các permission thuộc `EnabledFeatures` của Org**).
   * Nếu `permissionCode` không tồn tại $\rightarrow$ Trả về `403 PERMISSION_DENIED`.
3. **Gắn Lớp 3 — Data Scope:**
   * Trích xuất `data_scope` cao nhất tương ứng với `permissionCode` (ví dụ `ORGANIZATION` > `SCHOOL` > `DEPARTMENT` > `ASSIGNED_CLASS` > `SELF`) đưa vào `c.Locals("data_scope")` để tầng Repository tự động thêm điều kiện lọc `WHERE` phù hợp.

---

## 4. Đặc tả Chi tiết Các Nhóm API Lõi Đơn vị (`/api/v1/*`)

### 4.1 Nhóm 1: Xác thực & Ngữ cảnh Người dùng Đơn vị (`/api/v1/auth` & `/me`)

| Method | Endpoint | Mô tả & Logic xử lý |
| :--- | :--- | :--- |
| `POST` | `/api/v1/auth/login` | Nhận `subdomain`, `identifier` (email/username/phone), `password`. Xác minh Org `ACTIVE`, xác minh user thuộc Org đó qua `school_memberships`, cấp JWT (`realm: "tenant"`) + Refresh Token. |
| `GET` | `/api/v1/me` | Trả về thông tin `user`, thông tin `organization` hiện tại, danh sách `schools` mà user có quyền truy cập, `active_school_id`, danh sách `roles`, danh sách `permissions` hiệu lực, và **cây `menus` động đã lọc**. |
| `POST` | `/api/v1/auth/switch-school` | Chuyển đổi qua lại giữa các trường (`school_id`) nếu user thuộc nhiều trường trong cùng 1 Organization. |
| `POST` | `/api/v1/auth/logout` | Hủy session trên Redis và đưa `jti` vào blacklist. |

### 4.2 Nhóm 2: Thông tin Đơn vị & Quản lý Trường trực thuộc (`/api/v1/organization`, `/schools`)

| Method | Endpoint | Permission yêu cầu | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/organization` | Authenticated | Xem thông tin chung của Organization và trạng thái gói dịch vụ đang sử dụng (tên gói chính, Add-ons, ngày hết hạn, hạn mức quota còn lại). |
| `PATCH` | `/api/v1/organization` | `org:settings:update` | Cập nhật thông tin liên hệ, địa chỉ, logo của Đơn vị (không cho tự đổi `subdomain`). |
| `GET` | `/api/v1/schools` | `school:read` | Danh sách các trường (`schools`) thuộc Organization (tự động cô lập bởi RLS). |
| `POST` | `/api/v1/schools` | `school:create` | Tạo trường mới trực thuộc Organization. **Quota Guard:** Kiểm tra số lượng trường hiện tại không vượt quá `effective_quotas["max_schools"]` của gói dịch vụ. |
| `PUT` | `/api/v1/schools/:id` | `school:update` | Cập nhật thông tin trường (`name`, `education_levels`, `address`, `status`). |

### 4.3 Nhóm 3: Quản lý Nhân sự & Phân quyền tại Đơn vị (`/api/v1/iam/*`)

| Method | Endpoint | Permission yêu cầu | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/iam/members` | `iam:member:read` | Danh sách thành viên thuộc trường hiện tại (`school_memberships` join `users` và `roles`). |
| `POST` | `/api/v1/iam/members` | `iam:member:create` | Thêm tài khoản mới vào trường: Nếu `email/phone` đã có trong bảng `users` (ví dụ phụ huynh có con học trường khác trong hệ thống) thì tái sử dụng `user_id` (Single Account Identity), ngược lại tạo mới `users`, sau đó tạo `school_memberships` và gán `role_ids`. |
| `PUT` | `/api/v1/iam/members/:membership_id/roles` | `iam:member:assign_role` | Cập nhật danh sách vai trò (`role_ids`) cho thành viên. **Guard:** Cấm gỡ `SCHOOL_ADMIN` khỏi admin cuối cùng của trường. Xóa cache `tenant:authz:member:{membership_id}`. |
| `GET` | `/api/v1/iam/entitled-permissions` | `iam:role:read` | Lấy danh sách `permissions` từ Catalog mà **Organization hiện tại được phép dùng** (lọc theo `EnabledFeatures` của gói dịch vụ) để hiển thị trên UI tạo Role. |
| `GET` / `POST` | `/api/v1/iam/roles` | `iam:role:manage` | Xem và tạo vai trò tùy chỉnh của Đơn vị (`name`, `code`, `data_scope`, `permission_ids`). **Entitlement Guard:** Từ chối `403` nếu bất kỳ `permission_id` nào gửi lên không thuộc các Feature mà Organization đã mua. |
| `PUT` | `/api/v1/iam/roles/:id` | `iam:role:manage` | Cập nhật thông tin, `data_scope` và `permission_ids` của vai trò. Xóa cache Redis của toàn bộ thành viên giữ vai trò này. |

### 4.4 Nhóm 4: Tùy chỉnh Menu Đơn vị (`/api/v1/menus`)

| Method | Endpoint | Permission yêu cầu | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/menus/manage` | `org:menu:manage` | Lấy toàn bộ cây menu của Đơn vị (kết hợp `menus` nền của Platform + `organization_menu_configs`) để School Admin cấu hình. |
| `PUT` | `/api/v1/menus/customize` | `org:menu:manage` | Cho phép Đơn vị đổi tên hiển thị (`custom_label`), đổi icon (`custom_icon`), sắp xếp lại thứ tự (`sort_order`), hoặc ẩn các menu không bắt buộc (`is_mandatory == false`). |
| `POST` | `/api/v1/menus/custom-items` | `org:menu:manage` | Tạo lối tắt menu riêng trỏ tới một `route_id` hợp lệ nằm trong `tenant_routes` mà Organization có Entitlement. |

---

## 5. Seed Dữ liệu Mặc định khi Khởi tạo Đơn vị Mới (Onboarding Hook)

Bổ sung vào hàm `CreateOrganization` ở Platform Admin (hoặc chạy ngay khi duyệt Org):
1. Tự động tạo 01 vai trò hệ thống trong bảng `roles` của Organization mới:
   * `code`: `"SCHOOL_ADMIN"`, `name`: `"Quản trị viên Trường"`, `data_scope`: `"ORGANIZATION"`, `is_system`: `true`.
2. Gán mặc định toàn bộ các quyền lõi (`CORE_IAM`, `CORE_ORG`, `CORE_ACADEMIC`) vào vai trò `SCHOOL_ADMIN` này.
3. Gán vai trò `SCHOOL_ADMIN` cho tài khoản quản trị đầu tiên của trường trong `user_roles` để ngay khi bàn giao subdomain, khách hàng có thể đăng nhập vào `{subdomain}.school-erp.vn` và bắt đầu thiết lập cơ cấu trường học.

---

## 6. Chiến lược Redis Cache phía Tenant (`internal/pkg/rediskeys/tenant.go`)

| Redis Key Pattern | Kiểu dữ liệu | TTL | Sự kiện Invalidate (Xóa Cache) |
| :--- | :--- | :--- | :--- |
| `tenant:subdomain:{subdomain}` | `STRING` (JSON Org) | 30 phút | Platform duyệt/khóa Org, đổi subdomain |
| `tenant:user:orgs:{user_id}` | `SET` (Danh sách `org_id`) | 30 phút | Thêm/xóa user khỏi `school_memberships` |
| `tenant:authz:member:{membership_id}` | `HASH` (`perm_code -> data_scope`) | 15 phút | Đổi Role của member, sửa quyền của Role, hoặc Org hết hạn/đổi gói dịch vụ |
| `tenant:entitlement:{org_id}` | `STRING` (JSON Entitlement) | Smart TTL ($\le$ 30m) | Platform cấp/gia hạn/thu hồi gói dịch vụ |

---

## 7. Checklist Triển khai Code cho Backend Developer

1. [ ] **Tạo DB User riêng cho App:** Đảm bảo user kết nối DB của Go không phải `postgres` superuser và không có `BYPASSRLS`.
2. [ ] **Chạy Migration `000006`:** Tạo các bảng `schools`, `users`, `school_memberships`, `roles`, `role_permissions`, `user_roles`, `organization_menu_configs` kèm các policy RLS.
3. [ ] **Viết Test Cách ly RLS (Bắt buộc):** Tạo 2 Organization A và B; mở `WithTenantTx` với `org_id = A` và xác nhận câu lệnh `SELECT * FROM schools` không bao giờ trả về dữ liệu của Org B.
4. [ ] **Triển khai `TenantResolver` & `RequireTenantAccess`:** Hoàn thiện middleware xác minh chéo `X-Tenant-Subdomain` + JWT + Membership và kiểm tra chuỗi 3 lớp `Entitlement -> RBAC -> Data Scope`.
5. [ ] **Implement API `/api/v1/auth/login` & `/api/v1/me`:** Đảm bảo trả về cây menu động đã lọc đúng theo gói dịch vụ của Org và vai trò của User.
6. [ ] **Implement API Quản lý Trường, Nhân sự & Phân quyền đơn vị:** Hoàn thiện CRUD `schools`, `iam/members`, `iam/roles` và `menus`.