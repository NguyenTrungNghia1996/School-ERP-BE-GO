# TÀI LIỆU ĐẶC TẢ TRIỂN KHAI BACKEND — CATALOG, GÓI DỊCH VỤ & SUBSCRIPTION (PLATFORM ADMIN)

**Mã tài liệu:** `SPEC-BE-PLATFORM-PACKAGE-CATALOG`  
**Phiên bản:** 1.0  
**Trạng thái:** Ready for Coding (Hoàn thiện phần API còn lại của Platform Admin, đồng bộ với `SCHOOL_ERP_PROJECT_MASTER_v1.3` và `BACKEND_PLATFORM_ADMIN_SPEC_v1.0`)  
**Tech Stack:** Go (Golang), Fiber v2, GORM, PostgreSQL 16+, Redis 7+  
**Domain triển khai:** `api.school-erp.vn` (Namespace `/api/v1/platform/catalog/*`, `/packages/*`, `/subscriptions/*`)

---

## 1. Phạm vi & Nguyên tắc Kiến trúc Cốt lõi

Tài liệu này đặc tả toàn bộ phần Backend còn lại thuộc phạm vi quản trị của **Platform Admin**, đóng vai trò là nguồn cấp cấu hình tính năng (Feature), danh mục quyền (Permission Catalog), cây menu nền (Base Menu) và gói dịch vụ (Primary Package + Add-ons) xuống cho các Organization.

### 1.1 Các quy tắc bất biến (Invariants)
1. **Tách biệt khái niệm:** `Feature` (tính năng thương mại bán theo gói) $\neq$ `Permission` (mã quyền hành động `resource:action` để gán vào Role của trường) $\neq$ `Menu` (mục điều hướng UI). Một Feature có thể mở khóa nhiều Permission và nhiều Menu.
2. **Immutable Package Version:** Một `service_package_version` khi đang ở trạng thái `DRAFT` có thể chỉnh sửa danh sách Feature và Quota. Ngay khi gọi API `PUBLISH`, phiên bản này chuyển sang `PUBLISHED` và **tuyệt đối bất biến** (không được thêm/bớt Feature, không sửa Quota). Muốn thay đổi cấu trúc gói, Platform Admin phải tạo version mới (`v2`, `v3`...).
3. **1 Primary + N Add-ons:** Tại một thời điểm hiệu lực, mỗi Organization chỉ được phép có **tối đa 01 subscription gói chính (`PRIMARY`)** đang hoạt động (`ACTIVE`), kết hợp với **0 hoặc nhiều subscription gói bổ sung (`ADDON`)**.
4. **Hợp nhất Entitlement (Union Merge):** Tập tính năng (Features) và hạn mức (Quotas) mà một Organization được hưởng là hợp nhất từ gói chính và tất cả các Add-on đang trong trạng thái `ACTIVE` và thỏa mãn `started_at <= NOW() < expires_at`. Nếu gói chính và Add-on trùng Feature, Feature đó vẫn có hiệu lực cho đến khi **tất cả** các gói cấp Feature đó đều hết hạn hoặc bị thu hồi.
5. **Global Catalog vs. Tenant-Scoped Subscription:**
   * Nhóm bảng Catalog (`features`, `permissions`, `menus`, `menu_routes`) và Package (`service_packages`, `service_package_versions`, `service_package_features`) là dữ liệu toàn cục của nền tảng $\rightarrow$ **Không bật PostgreSQL RLS theo `organization_id`**.
   * Nhóm bảng Subscription (`organization_subscriptions`, `subscription_items`) mang `organization_id` $\rightarrow$ **Bắt buộc bật PostgreSQL RLS** (Platform Admin thao tác qua DB role đặc quyền quản trị hoặc transaction có set context).

---

## 2. Thiết kế Cơ sở dữ liệu PostgreSQL (DDL & Constraints)

### 2.1 Migration `000003_init_tenant_catalog.up.sql` (Catalog Tính năng, Quyền & Menu nền)

```sql
-- 1. Danh mục Tính năng thương mại (Features)
CREATE TABLE features (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(80) NOT NULL,                 -- VD: 'CORE_ACADEMIC', 'ATTENDANCE', 'FINANCE_FEE', 'LIBRARY'
    module VARCHAR(50) NOT NULL,               -- Phân nhóm nghiệp vụ: CORE, ACADEMIC, STUDENT, HR, FINANCE, OPS
    name VARCHAR(150) NOT NULL,
    description TEXT,
    is_core BOOLEAN NOT NULL DEFAULT FALSE,    -- TRUE nếu là tính năng lõi bắt buộc mọi gói chính đều nên có
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_features_code ON features (UPPER(code));

-- 2. Danh mục Quyền chuẩn của Tenant (Permission Catalog)
CREATE TABLE permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_id UUID NOT NULL REFERENCES features(id) ON DELETE RESTRICT,
    code VARCHAR(100) NOT NULL,                -- Chuẩn resource:action (VD: 'attendance:read', 'fee:collect')
    resource VARCHAR(60) NOT NULL,             -- VD: 'attendance', 'fee'
    action VARCHAR(40) NOT NULL,               -- VD: 'read', 'create', 'update', 'delete', 'approve', 'export'
    name VARCHAR(150) NOT NULL,
    description TEXT,
    is_sensitive BOOLEAN NOT NULL DEFAULT FALSE, -- Đánh dấu quyền nhạy cảm (xóa điểm, hoàn học phí...)
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_permissions_code ON permissions (LOWER(code));
CREATE INDEX idx_permissions_feature ON permissions (feature_id);

-- 3. Route Registry cho Tenant Frontend (Đảm bảo menu chỉ trỏ tới route hợp lệ)
CREATE TABLE tenant_routes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_key VARCHAR(100) NOT NULL UNIQUE,    -- VD: 'academic.classes.list'
    path VARCHAR(255) NOT NULL UNIQUE,         -- VD: '/academic/classes'
    feature_id UUID REFERENCES features(id) ON DELETE RESTRICT, -- NULL nếu là route chung (VD: /dashboard, /profile)
    name VARCHAR(150) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 4. Cây Menu nền mặc định cho Tenant (Platform Admin cấu hình, Organization kế thừa & tùy chỉnh)
CREATE TABLE menus (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id UUID REFERENCES menus(id) ON DELETE CASCADE,
    menu_key VARCHAR(100) NOT NULL UNIQUE,
    label VARCHAR(120) NOT NULL,
    icon VARCHAR(60),
    menu_type VARCHAR(20) NOT NULL DEFAULT 'ITEM' CHECK (menu_type IN ('GROUP', 'SUBMENU', 'ITEM')),
    route_id UUID REFERENCES tenant_routes(id) ON DELETE RESTRICT,
    feature_id UUID REFERENCES features(id) ON DELETE RESTRICT, -- Điều kiện Entitlement để hiển thị menu
    required_permissions JSONB NOT NULL DEFAULT '[]'::jsonb,     -- Mảng mã permission: ["attendance:read"]
    permission_match_mode VARCHAR(10) NOT NULL DEFAULT 'ANY' CHECK (permission_match_mode IN ('ANY', 'ALL')),
    target_portals JSONB NOT NULL DEFAULT '["SCHOOL_ADMIN"]'::jsonb, -- SCHOOL_ADMIN, TEACHER, STAFF, STUDENT, FAMILY
    is_mandatory BOOLEAN NOT NULL DEFAULT FALSE, -- TRUE: Organization không được phép ẩn menu này
    sort_order INT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_menus_parent_sort ON menus (parent_id, sort_order);
```

### 2.2 Migration `000004_init_packages_and_subscriptions.up.sql` (Gói dịch vụ & Đăng ký)

Sử dụng extension `btree_gist` để PostgreSQL tự động chặn lỗi trùng lặp thời gian (overlapping) của 2 gói chính (`PRIMARY`) trên cùng một `organization_id` ở tầng Database.

```sql
CREATE EXTENSION IF NOT EXISTS "btree_gist";

-- 5. Định nghĩa Gói dịch vụ (Package Master)
CREATE TABLE service_packages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(60) NOT NULL,                 -- VD: 'PKG_BASIC', 'PKG_PRO', 'ADDON_FINANCE', 'ADDON_LIBRARY'
    name VARCHAR(150) NOT NULL,
    description TEXT,
    package_type VARCHAR(20) NOT NULL CHECK (package_type IN ('PRIMARY', 'ADDON')),
    is_public BOOLEAN NOT NULL DEFAULT TRUE,   -- FALSE nếu là gói thiết kế riêng (Enterprise Custom)
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
    version_number INT NOT NULL,               -- 1, 2, 3...
    version_label VARCHAR(40) NOT NULL,        -- VD: 'v1.0 - 2026'
    status VARCHAR(20) NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'PUBLISHED', 'DEPRECATED')),
    billing_cycle VARCHAR(20) NOT NULL DEFAULT 'YEARLY' CHECK (billing_cycle IN ('MONTHLY', 'YEARLY', 'CUSTOM')),
    base_price NUMERIC(15, 2) NOT NULL DEFAULT 0,
    currency VARCHAR(10) NOT NULL DEFAULT 'VND',
    quotas JSONB NOT NULL DEFAULT '{}'::jsonb, -- VD: {"max_schools": 3, "max_students": 2000, "max_storage_gb": 50}
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
    feature_limits JSONB NOT NULL DEFAULT '{}'::jsonb, -- Giới hạn riêng của feature nếu có
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
    custom_quotas JSONB,                       -- Ghi đè quota nếu thỏa thuận riêng cho Org này
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
    USING (organization_id = NULLIF(current_setting('app.organization_id', TRUE), '')::UUID);
```

---

## 3. Tính toán Entitlement & Chiến lược Redis Cache

### 3.1 Cấu trúc dữ liệu Entitlement của một Organization

Khi hệ thống (cả Platform Admin lúc xem chi tiết Org lẫn Tenant Backend lúc kiểm tra quyền truy cập API) cần biết Organization hiện tại được dùng những tính năng gì, Backend tổng hợp thành struct `OrganizationEntitlement`:

```go
type OrganizationEntitlement struct {
	OrganizationID    string                 `json:"organization_id"`
	PrimaryPackage    *ActivePackageSummary  `json:"primary_package"`
	ActiveAddons      []ActivePackageSummary `json:"active_addons"`
	EnabledFeatures   []string               `json:"enabled_features"`   // Danh sách feature.code đã deduplicate
	EffectiveQuotas   map[string]int64       `json:"effective_quotas"`   // Cộng dồn hoặc lấy MAX tùy loại quota
	CalculatedAt      time.Time              `json:"calculated_at"`
	NearestExpiryAt   *time.Time             `json:"nearest_expiry_at"`  // Thời điểm hết hạn gần nhất của 1 gói bất kỳ
}
```

### 3.2 Quy tắc Cache Redis cho Entitlement & Catalog

1. **Key Cache Entitlement:** `tenant:entitlement:{organization_id}` (Lưu chuỗi JSON của `OrganizationEntitlement`).
2. **Tính toán TTL thông minh (Smart TTL):**
   * Mặc định TTL là `30 phút` (`30 * time.Minute`).
   * Tuy nhiên, nếu `NearestExpiryAt` của một gói dịch vụ nằm trong khoảng dưới 30 phút tới (ví dụ còn 7 phút nữa Add-on hết hạn), set TTL bằng đúng `NearestExpiryAt - time.Now()` để Redis tự động hủy cache đúng khoảnh khắc gói hết hạn!
3. **Cache Invalidation bắt buộc:**
   * Ngay sau khi Platform Admin thực hiện **Cấp gói mới (`Assign`)**, **Gia hạn (`Renew`)**, **Đổi gói (`Upgrade/Downgrade`)**, **Tạm ngưng (`Suspend`)** hoặc **Thu hồi (`Revoke`)** trên `organization_subscriptions` $\rightarrow$ Gọi `DEL tenant:entitlement:{organization_id}` và `DEL tenant:menus:{organization_id}:*` (nếu có cache menu đã render).
   * Ngoài ra, chạy một Background Worker (Cronjob mỗi 5 phút) quét các bản ghi `organization_subscriptions` có `status = 'ACTIVE'` và `expires_at <= NOW()` để cập nhật sang `status = 'EXPIRED'` và xóa cache tương ứng.

---

## 4. Đặc tả Chi tiết API (`/api/v1/platform/*`)

Tất cả các endpoint dưới đây đều nằm sau `PlatformAuthMiddleware` (`realm == "platform"`) và được bảo vệ bởi `RequirePlatformPermission(...)`.

### 4.1 Nhóm API Quản lý Catalog (Features, Permissions, Base Menus)

| Method | Endpoint | Permission | Mô tả & Quy tắc nghiệp vụ |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/platform/catalog/features` | `platform:feature:manage` | Danh sách `features`, hỗ trợ filter `module`, `is_active`, search theo `code`/`name`. |
| `POST` | `/api/v1/platform/catalog/features` | `platform:feature:manage` | Tạo Feature mới. Validate `code` uppercase (`^[A-Z0-9_]+$`), unique. Ghi Audit Log. |
| `PUT` | `/api/v1/platform/catalog/features/:id` | `platform:feature:manage` | Cập nhật `name`, `description`, `module`, `is_active`. **Không cho sửa `code`** nếu Feature đã được gán vào bất kỳ `service_package_version` nào. |
| `GET` | `/api/v1/platform/catalog/permissions` | `platform:permission:manage` | Danh sách `permissions` kèm thông tin `feature` cha. Filter theo `feature_id`, `resource`, `is_sensitive`. |
| `POST` | `/api/v1/platform/catalog/permissions` | `platform:permission:manage` | Tạo Permission mới gắn với `feature_id`. Validate `code` chuẩn `resource:action` (`^[a-z0-9_]+:[a-z0-9_]+$`). |
| `PUT` | `/api/v1/platform/catalog/permissions/:id` | `platform:permission:manage` | Cập nhật mô tả, `is_sensitive`, `is_active`. Không cho sửa `code`. |
| `GET` | `/api/v1/platform/catalog/menus/tree` | `platform:menu:manage` | Lấy toàn bộ cây `menus` nền của Tenant (dạng tree lồng nhau) để hiển thị trên màn hình Menu Builder của Platform Admin. |
| `POST` | `/api/v1/platform/catalog/menus` | `platform:menu:manage` | Thêm node menu mới (`GROUP`, `SUBMENU`, `ITEM`). Validate: nếu `menu_type == 'ITEM'` thì bắt buộc phải chọn `route_id` hợp lệ trong `tenant_routes`. |
| `PUT` | `/api/v1/platform/catalog/menus/reorder` | `platform:menu:manage` | Cập nhật hàng loạt `parent_id` và `sort_order` khi Platform Admin kéo-thả cây menu trên UI. |
| `GET` | `/api/v1/platform/catalog/routes` | `platform:menu:manage` | Danh sách `tenant_routes` đã đăng ký để chọn khi tạo menu. |

### 4.2 Nhóm API Quản lý Gói dịch vụ & Phiên bản (Packages & Versions)

| Method | Endpoint | Permission | Mô tả & Quy tắc nghiệp vụ |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/platform/packages` | `platform:package:read` | Danh sách `service_packages` kèm phiên bản `PUBLISHED` mới nhất và số lượng Organization đang sử dụng. |
| `POST` | `/api/v1/platform/packages` | `platform:package:manage` | Tạo gói dịch vụ mới (`code`, `name`, `package_type`: `PRIMARY` \| `ADDON`, `is_public`). |
| `GET` | `/api/v1/platform/packages/:id` | `platform:package:read` | Chi tiết gói dịch vụ và danh sách tất cả các phiên bản (`versions`). |
| `POST` | `/api/v1/platform/packages/:id/versions` | `platform:package:manage` | Tạo phiên bản mới ở trạng thái `DRAFT`. Hỗ trợ truyền `clone_from_version_id` để nhân bản nhanh danh sách Feature và Quotas từ bản cũ. |
| `PUT` | `/api/v1/platform/package-versions/:version_id` | `platform:package:manage` | Cập nhật thông tin phiên bản (`version_label`, `billing_cycle`, `base_price`, `quotas`, `feature_ids`). **Guard:** Trả lỗi `409 PACKAGE_VERSION_IMMUTABLE` nếu `status != 'DRAFT'`. |
| `POST` | `/api/v1/platform/package-versions/:version_id/publish` | `platform:package:manage` | Chuyển trạng thái từ `DRAFT` $\rightarrow$ `PUBLISHED`. Validate phải có ít nhất 1 `feature_id` (đối với gói `ADDON`). Ghi nhận `published_at`, `published_by` và Audit Log. |
| `POST` | `/api/v1/platform/package-versions/:version_id/deprecate` | `platform:package:manage` | Chuyển `PUBLISHED` $\rightarrow$ `DEPRECATED` (ngừng bán mới, các trường đang dùng vẫn tiếp tục chạy đến hết chu kỳ). |

### 4.3 Nhóm API Cấp & Quản lý Subscription cho Organization

| Method | Endpoint | Permission | Mô tả & Quy tắc nghiệp vụ |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/platform/organizations/:org_id/subscriptions` | `platform:org:read` | Lịch sử và danh sách toàn bộ gói chính + Add-ons của Organization. |
| `GET` | `/api/v1/platform/organizations/:org_id/entitlements` | `platform:org:read` | Xem kết quả tính toán Entitlement hợp nhất hiện tại (bao gồm danh sách Feature đang mở khóa và Quota tổng hợp) để Platform Admin kiểm tra. |
| `POST` | `/api/v1/platform/organizations/:org_id/subscriptions` | `platform:subscription:manage` | **Cấp gói mới (Primary hoặc Add-on):**<br>1. Kiểm tra `package_version_id` phải đang ở trạng thái `PUBLISHED`.<br>2. Nếu `package_type == 'PRIMARY'`, kiểm tra không bị trùng thời gian với gói chính hiện có (DB Exclusion Constraint sẽ bắt nếu có race condition).<br>3. Nếu `started_at <= NOW()`, set `status = 'ACTIVE'`; nếu `started_at > NOW()`, set `status = 'SCHEDULED'`.<br>4. Ghi Audit Log & xóa cache `tenant:entitlement:{org_id}`. |
| `POST` | `/api/v1/platform/subscriptions/:sub_id/renew` | `platform:subscription:manage` | **Gia hạn gói dịch vụ:**<br>- Nếu gia hạn nối tiếp trên cùng `package_version_id`: cộng dồn `expires_at` hoặc tạo bản ghi subscription nối tiếp với `started_at = old_sub.expires_at`.<br>- Nếu gia hạn sang `package_version_id` mới hơn: tạo bản ghi nối tiếp với `started_at = old_sub.expires_at`. |
| `POST` | `/api/v1/platform/organizations/:org_id/switch-primary` | `platform:subscription:manage` | **Đổi gói chính giữa chu kỳ (Upgrade/Downgrade):**<br>Thực thi trong 1 DB Transaction:<br>1. Kết thúc gói chính cũ tại thời điểm `NOW()` (`expires_at = NOW()`, `status = 'EXPIRED'`).<br>2. Tạo bản ghi gói chính mới bắt đầu từ `NOW()` đến `new_expires_at`.<br>3. Ghi Audit Log & xóa ngay cache `tenant:entitlement:{org_id}`. |
| `PATCH` | `/api/v1/platform/subscriptions/:sub_id/suspend` | `platform:subscription:manage` | Tạm ngưng gói dịch vụ (`status = 'SUSPENDED'`, bắt buộc `reason`). Xóa ngay cache `tenant:entitlement:{org_id}`. |
| `PATCH` | `/api/v1/platform/subscriptions/:sub_id/revoke` | `platform:subscription:manage` | Thu hồi vĩnh viễn gói dịch vụ trước hạn (`status = 'REVOKED'`, `revoked_at = NOW()`, bắt buộc `reason`). Xóa ngay cache `tenant:entitlement:{org_id}`. |

---

## 5. Payload Mẫu Cho Các Nghiệp Vụ Quan Trọng

### 5.1 Payload Tạo/Cập nhật Phiên bản Gói (`PUT /api/v1/platform/package-versions/:version_id`)

```json
{
  "version_label": "v1.0 - Năm học 2026-2027",
  "billing_cycle": "YEARLY",
  "base_price": 12000000,
  "currency": "VND",
  "quotas": {
    "max_schools": 1,
    "max_students": 1500,
    "max_staff": 150,
    "max_storage_gb": 50
  },
  "feature_ids": [
    "550e8400-e29b-41d4-a716-446655440001",
    "550e8400-e29b-41d4-a716-446655440002"
  ],
  "release_notes": "Gói tiêu chuẩn dành cho trường phổ thông 1 cơ sở"
}
```

### 5.2 Payload Cấp Gói Dịch Vụ Cho Tổ Chức (`POST /api/v1/platform/organizations/:org_id/subscriptions`)

```json
{
  "package_version_id": "770e8400-e29b-41d4-a716-446655440099",
  "started_at": "2026-11-15T00:00:00Z",
  "expires_at": "2027-11-15T00:00:00Z",
  "custom_quotas": {
    "max_students": 2000
  },
  "auto_renew": true,
  "notes": "Hợp đồng số 102/HĐ-SaaS ký ngày 13/11/2026"
}
```

### 5.3 Response Xem Entitlement Hợp Nhất (`GET /api/v1/platform/organizations/:org_id/entitlements`)

```json
{
  "success": true,
  "data": {
    "organization_id": "880e8400-e29b-41d4-a716-446655440010",
    "primary_package": {
      "subscription_id": "990e8400-e29b-41d4-a716-446655440020",
      "package_code": "PKG_STANDARD",
      "package_name": "Gói Tiêu Chuẩn",
      "version_number": 1,
      "expires_at": "2027-11-15T00:00:00Z"
    },
    "active_addons": [
      {
        "subscription_id": "990e8400-e29b-41d4-a716-446655440021",
        "package_code": "ADDON_FINANCE",
        "package_name": "Phân hệ Tài chính & Học phí",
        "version_number": 2,
        "expires_at": "2027-05-15T00:00:00Z"
      }
    ],
    "enabled_features": [
      "CORE_ACADEMIC",
      "STUDENT_PROFILE",
      "ATTENDANCE",
      "GRADEBOOK",
      "FINANCE_FEE"
    ],
    "effective_quotas": {
      "max_schools": 1,
      "max_students": 2000,
      "max_staff": 150,
      "max_storage_gb": 50
    },
    "nearest_expiry_at": "2027-05-15T00:00:00Z"
  }
}
```

---

## 6. Xử lý Lỗi Đặc thù & Checklist Triển khai

### 6.1 Mã lỗi bổ sung cho Module Package & Subscription

| HTTP Status | Error Code | Tình huống phát sinh |
| :--- | :--- | :--- |
| `409` | `PRIMARY_SUBSCRIPTION_OVERLAP` | Cấp gói `PRIMARY` có khoảng thời gian `[started_at, expires_at)` chồng lấn với một gói `PRIMARY` đang hoạt động/lên lịch khác của cùng Organization (Bắt lỗi PostgreSQL `23P01` `exclusion_violation`). |
| `409` | `PACKAGE_VERSION_IMMUTABLE` | Cố tình gọi `PUT` để sửa đổi `service_package_version` đang ở trạng thái `PUBLISHED` hoặc `DEPRECATED`. |
| `422` | `PACKAGE_VERSION_NOT_PUBLISHED` | Cố tình gán một `service_package_version` đang ở trạng thái `DRAFT` hoặc `DEPRECATED` cho Organization. |
| `422` | `FEATURE_IN_USE_CANNOT_MODIFY` | Cố tình xóa hoặc đổi `code` của một `feature` đã được gắn vào gói dịch vụ hoặc đang có `permissions` tham chiếu tới. |

### 6.2 Checklist Hoàn thiện Backend Platform Admin

1. [ ] **Chạy Migration:** Thực thi `000003_init_tenant_catalog.up.sql` và `000004_init_packages_and_subscriptions.up.sql` (đảm bảo DB user có quyền tạo extension `btree_gist`).
2. [ ] **Seed Catalog & Gói mẫu:** Tạo script seed các `features` lõi (`CORE_ACADEMIC`, `STUDENT_PROFILE`, `HR_STAFF`, `ATTENDANCE`, `GRADEBOOK`, `FINANCE_FEE`), bộ `permissions` tương ứng (`resource:action`), `tenant_routes` và cây `menus` nền mặc định.
3. [ ] **Implement Service `EntitlementCalculator`:** Viết hàm dùng chung giữa Platform Admin và Tenant Backend để tính toán và cache `OrganizationEntitlement` vào Redis với **Smart TTL**.
4. [ ] **Implement API Package Builder & Versioning:** Đảm bảo khóa cứng mọi thao tác sửa đổi sau khi `PUBLISH` và hỗ trợ `clone_from_version_id`.
5. [ ] **Implement API Subscription Lifecycle:** Đảm bảo bắt lỗi PostgreSQL `23P01` (Exclusion Violation) trả về `409 PRIMARY_SUBSCRIPTION_OVERLAP`, thực hiện Atomic Switch khi đổi gói chính giữa chu kỳ, và xóa cache Redis ngay sau mỗi mutation.
6. [ ] **Background Job Hết hạn Gói:** Cấu hình goroutine ticker hoặc asynq job chạy định kỳ 5 phút/lần để chuyển trạng thái các gói quá hạn sang `EXPIRED` và làm sạch cache.