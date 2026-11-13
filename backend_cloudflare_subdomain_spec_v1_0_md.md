# TÀI LIỆU ĐẶC TẢ TÍCH HỢP CLOUDFLARE DNS — TỰ ĐỘNG CẤP PHÁT & CHỈNH SỬA SUBDOMAIN

**Mã tài liệu:** `SPEC-BE-CLOUDFLARE-SUBDOMAIN`  
**Phiên bản:** 1.0  
**Trạng thái:** Ready for Coding (Bổ sung cho `BACKEND_PLATFORM_ADMIN_SPEC_v1.0` & `SCHOOL_ERP_PROJECT_MASTER_v1.3`)  
**Tech Stack:** Go (Golang), Fiber v2, GORM, PostgreSQL 16+, Redis 7+, Cloudflare API v4  

---

## 1. Kiến trúc & Nguyên tắc Tích hợp Cloudflare

### 1.1 Mô hình hạ tầng DNS & SSL
- **Root Domain:** `school-erp.vn` được quản lý DNS trên Cloudflare (Proxy bật màu cam — `proxied: true` để tận dụng DDoS protection, WAF và Universal SSL / Advanced Certificate Manager).
- **Trỏ dịch vụ:** Mọi subdomain của Organization (`{subdomain}.school-erp.vn`) đều tạo bản ghi **`CNAME`** trỏ về Frontend Ingress/Load Balancer (ví dụ: `tenant-ingress.school-erp.vn` hoặc `cname.vercel-dns.com` / Nginx Ingress).
- **Bảo mật API Token:** Sử dụng Cloudflare **Scoped API Token** (chỉ cấp quyền `Zone:DNS:Edit` trên đúng `CLOUDFLARE_ZONE_ID` của `school-erp.vn`), tuyệt đối không dùng Global API Key.

### 1.2 Nguyên tắc Transactional Outbox & Bất đồng bộ (Async Provisioning)
- **Không gọi Cloudflare HTTP API bên trong DB Transaction:** Nếu Cloudflare API chậm hoặc lỗi mạng, việc giữ DB transaction sẽ gây lock bảng `organizations`.
- **Cơ chế State Machine (`dns_status`):**
  1. Khi Platform Admin tạo mới, phê duyệt hoặc đổi subdomain của Organization, DB commit ngay lập tức với trạng thái `dns_status = 'PROVISIONING'`.
  2. Hệ thống đẩy một job vào Queue (Redis Stream / RabbitMQ / Go Worker kèm retry backoff) để gọi Cloudflare API tạo/cập nhật bản ghi `CNAME`.
  3. Khi Cloudflare trả về `record.id`, Worker cập nhật `cloudflare_dns_record_id` và chuyển `dns_status = 'ACTIVE'`. Nếu lỗi sau 3 lần thử, chuyển sang `dns_status = 'FAILED'` để Admin bấm nút "Thử lại (Retry DNS)" trên UI.

---

## 2. Cập nhật Cơ sở dữ liệu PostgreSQL (Migration `000005_add_cloudflare_dns_tracking.up.sql`)

Bổ sung các cột theo dõi trạng thái đồng bộ Cloudflare vào bảng `organizations` và bảng lịch sử `organization_subdomain_histories`:

```sql
-- 1. Bổ sung trường quản lý Cloudflare DNS trên bảng organizations
ALTER TABLE organizations
    ADD COLUMN cloudflare_dns_record_id VARCHAR(64),
    ADD COLUMN dns_status VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CHECK (dns_status IN ('PENDING', 'PROVISIONING', 'ACTIVE', 'FAILED', 'DELETING')),
    ADD COLUMN dns_last_error TEXT,
    ADD COLUMN dns_synced_at TIMESTAMPTZ;

CREATE INDEX idx_organizations_dns_status ON organizations (dns_status) WHERE dns_status IN ('PROVISIONING', 'FAILED');

-- 2. Bổ sung tracking Cloudflare cho lịch sử đổi subdomain (hỗ trợ Redirect 301/308 subdomain cũ)
ALTER TABLE organization_subdomain_histories
    ADD COLUMN old_cloudflare_record_id VARCHAR(64),
    ADD COLUMN old_record_status VARCHAR(20) NOT NULL DEFAULT 'REDIRECTING'
        CHECK (old_record_status IN ('REDIRECTING', 'DELETED', 'CLEANUP_FAILED')),
    ADD COLUMN cleaned_up_at TIMESTAMPTZ;
```

---

## 3. Biến môi trường & Cấu hình (`config/cloudflare.go`)

```env
CLOUDFLARE_API_TOKEN=cf_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
CLOUDFLARE_ZONE_ID=023e105f4ecef8ad9ca31a8372d0c353
CLOUDFLARE_ROOT_DOMAIN=school-erp.vn
CLOUDFLARE_TENANT_TARGET=tenant-ingress.school-erp.vn
CLOUDFLARE_PROXIED=true
CLOUDFLARE_TTL=1 # 1 = Automatic (bắt buộc khi proxied = true)
```

---

## 4. Triển khai Cloudflare DNS Client & Service trong Go

### 4.1 Cấu trúc thư mục bổ sung
```text
internal/
├── pkg/
│   └── cloudflare/
│       └── dns_client.go           # Wrapper gọi Cloudflare API v4 (Create, Update, Delete, GetByName)
└── platform/
    └── tenant_mgmt/
        ├── dns_provisioner.go      # Worker xử lý cấp phát, đổi subdomain, cleanup & retry
        └── handler_dns.go          # API kiểm tra trạng thái DNS & nút Retry thủ công
```

### 4.2 Interface & Implementation `internal/pkg/cloudflare/dns_client.go`

```go
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`    // "CNAME"
	Name    string `json:"name"`    // VD: "thpt-chu-van-an.school-erp.vn"
	Content string `json:"content"` // VD: "tenant-ingress.school-erp.vn"
	TTL     int    `json:"ttl"`     // 1 = Auto
	Proxied bool   `json:"proxied"` // true
	Comment string `json:"comment,omitempty"`
}

type cfAPIResponse[T any] struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result T `json:"result"`
}

type Client struct {
	apiToken   string
	zoneID     string
	rootDomain string
	targetHost string
	proxied    bool
	httpClient *http.Client
}

func NewClient(apiToken, zoneID, rootDomain, targetHost string, proxied bool) *Client {
	return &Client{
		apiToken:   apiToken,
		zoneID:     zoneID,
		rootDomain: rootDomain,
		targetHost: targetHost,
		proxied:    proxied,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// CreateSubdomainCNAME tạo mới bản ghi CNAME trên Cloudflare (Idempotent: nếu đã tồn tại thì lấy ID)
func (c *Client) CreateSubdomainCNAME(ctx context.Context, subdomain, orgID string) (string, error) {
	fqdn := fmt.Sprintf("%s.%s", subdomain, c.rootDomain)

	// 1. Kiểm tra xem bản ghi đã tồn tại trên Cloudflare chưa (tránh lỗi trùng khi retry)
	existing, err := c.FindRecordByFQDN(ctx, fqdn)
	if err == nil && existing != nil {
		return existing.ID, nil
	}

	payload := DNSRecord{
		Type:    "CNAME",
		Name:    fqdn,
		Content: c.targetHost,
		TTL:     1,
		Proxied: c.proxied,
		Comment: fmt.Sprintf("SchoolERP Tenant OrgID: %s", orgID),
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", c.zoneID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var cfResp cfAPIResponse[DNSRecord]
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return "", err
	}
	if !cfResp.Success {
		return "", fmt.Errorf("cloudflare create DNS failed: %+v", cfResp.Errors)
	}
	return cfResp.Result.ID, nil
}

// DeleteRecord xóa bản ghi DNS khỏi Cloudflare theo Record ID
func (c *Client) DeleteRecord(ctx context.Context, recordID string) error {
	if recordID == "" {
		return nil
	}
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", c.zoneID, recordID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Nếu 404 (đã bị xóa trước đó trên Cloudflare) thì coi như thành công
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}

	var cfResp cfAPIResponse[map[string]any]
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return err
	}
	if !cfResp.Success {
		return fmt.Errorf("cloudflare delete DNS failed: %+v", cfResp.Errors)
	}
	return nil
}

func (c *Client) FindRecordByFQDN(ctx context.Context, fqdn string) (*DNSRecord, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=CNAME&name=%s", c.zoneID, fqdn)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var cfResp cfAPIResponse[[]DNSRecord]
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, err
	}
	if cfResp.Success && len(cfResp.Result) > 0 {
		return &cfResp.Result[0], nil
	}
	return nil, fmt.Errorf("record not found")
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")
}
```

---

## 5. Luồng Nghiệp vụ Cấp phát, Chỉnh sửa & Thu hồi Subdomain

### 5.1 Luồng 1: Cấp phát Subdomain mới (Khi Tạo hoặc Phê duyệt Organization)
1. **Validation trước khi lưu DB:**
   - Regex: `^[a-z0-9]([a-z0-9-]{1,38}[a-z0-9])?$` (3–40 ký tự, không bắt đầu/kết thúc bằng `-`).
   - Kiểm tra không trùng `reserved_subdomains`.
   - Kiểm tra không trùng `organizations.subdomain` và không nằm trong các `old_subdomain` đang còn hạn `redirect_until > NOW()` tại `organization_subdomain_histories`.
   - *(Tùy chọn)* Gọi `cfClient.FindRecordByFQDN` để đảm bảo chưa có bản ghi DNS thủ công nào chiếm dụng trên Cloudflare.
2. **Cập nhật DB & Đẩy Job:**
   - Lưu `organizations` với `status = 'ACTIVE'`, `dns_status = 'PROVISIONING'`.
   - Đẩy sự kiện `DNS_PROVISION_ORG` vào Worker.
3. **Worker thực thi:**
   - Gọi `cfClient.CreateSubdomainCNAME(ctx, org.Subdomain, org.ID)`.
   - Thành công $\rightarrow$ Cập nhật `cloudflare_dns_record_id = recordID`, `dns_status = 'ACTIVE'`, `dns_synced_at = NOW()`, `dns_last_error = NULL`. Ghi Audit Log (`ACTION = 'CLOUDFLARE_DNS_PROVISIONED'`).
   - Thất bại (sau 3 lần retry với exponential backoff 2s, 5s, 15s) $\rightarrow$ Cập nhật `dns_status = 'FAILED'`, `dns_last_error = err.Error()`.

### 5.2 Luồng 2: Chỉnh sửa / Đổi Subdomain (`POST /api/v1/platform/organizations/:id/change-subdomain`)
Để đảm bảo người dùng truy cập link cũ (`old.school-erp.vn`) không bị lỗi `NXDOMAIN` (không tìm thấy trang) ngay lập tức và được tự động điều hướng sang `new.school-erp.vn` theo đúng **Mục 3.1** của tài liệu Master:

1. **Trong DB Transaction:**
   - Cập nhật `organizations.subdomain = newSubdomain`, `cloudflare_dns_record_id = NULL`, `dns_status = 'PROVISIONING'`.
   - Thêm bản ghi vào `organization_subdomain_histories`:
     - `old_subdomain`: tên miền cũ.
     - `new_subdomain`: tên miền mới.
     - `old_cloudflare_record_id`: ID bản ghi DNS cũ trên Cloudflare (**giữ nguyên bản ghi cũ trên Cloudflare**, chưa xóa ngay).
     - `redirect_until`: `NOW() + INTERVAL '30 days'` (hoặc số ngày Admin chọn).
   - Ghi Audit Log (`ACTION = 'ORG_SUBDOMAIN_CHANGED'`).
2. **Sau Commit DB:**
   - Xóa Redis cache: `DEL tenant:subdomain:{oldSubdomain}` và `DEL tenant:subdomain:{newSubdomain}`.
   - Đẩy job tạo bản ghi CNAME mới cho `newSubdomain` trên Cloudflare.
3. **Cơ chế Redirect 301/308 ở tầng ứng dụng:**
   - Khi trình duyệt truy cập `{oldSubdomain}.school-erp.vn`, DNS trên Cloudflare vẫn trỏ về Ingress của bạn.
   - Frontend / Edge Middleware gọi API `GET /api/v1/public/tenant-resolve?subdomain={host_subdomain}`.
   - Backend tra bảng `organizations` không thấy, tra tiếp `organization_subdomain_histories` thấy `old_subdomain` còn hạn `redirect_until > NOW()` $\rightarrow$ Trả về `{ "action": "REDIRECT", "target_url": "https://{newSubdomain}.school-erp.vn" }` để trình duyệt tự động chuyển hướng.
4. **Dọn dẹp tự động (Background Cronjob mỗi ngày lúc 02:00 sáng):**
   - Quét `organization_subdomain_histories` có `old_record_status = 'REDIRECTING'` và `redirect_until <= NOW()`.
   - Gọi `cfClient.DeleteRecord(ctx, history.OldCloudflareRecordID)` để giải phóng subdomain cũ trên Cloudflare và cập nhật `old_record_status = 'DELETED'`, `cleaned_up_at = NOW()`.

### 5.3 Luồng 3: Xóa / Hủy vĩnh viễn Organization
- Khi một Organization bị hủy vĩnh viễn (không còn sử dụng dịch vụ), Worker gọi `cfClient.DeleteRecord(ctx, org.CloudflareDNSRecordID)` và dọn sạch toàn bộ bản ghi `REDIRECTING` liên quan trên Cloudflare.

---

## 6. Bổ sung Endpoints API (`/api/v1/platform/organizations`)

| Method | Endpoint | Permission | Mô tả & Logic xử lý |
|---|---|---|---|
| `GET` | `/api/v1/platform/organizations/check-subdomain` | `platform:org:read` | Kiểm tra nhanh tính khả dụng của subdomain (`?subdomain=abc`): check regex, `reserved_subdomains`, DB `organizations`, `organization_subdomain_histories` và kiểm tra trực tiếp trên Cloudflare API. |
| `POST` | `/api/v1/platform/organizations/:id/dns/retry` | `platform:subdomain:manage` | Cho phép Platform Admin kích hoạt lại tiến trình đồng bộ DNS lên Cloudflare khi `dns_status == 'FAILED'`. |
| `DELETE` | `/api/v1/platform/subdomain-histories/:history_id/dns` | `platform:subdomain:manage` | Ngắt redirect sớm và xóa ngay bản ghi DNS của subdomain cũ trên Cloudflare trước thời hạn `redirect_until`. |
| `GET` | `/api/v1/public/tenant-resolve` | Public (Rate-limited) | API công khai (có cache Redis 30 phút) để Frontend xác định trạng thái của một subdomain: `ACTIVE` (trả về `org_id`, `name`, `logo`), `SUSPENDED`, `REDIRECT` (trả về URL mới), hoặc `NOT_FOUND`. |