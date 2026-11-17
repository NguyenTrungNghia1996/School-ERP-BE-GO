# TÀI LIỆU ĐẶC TẢ TRIỂN KHAI BACKEND — QUẢN LÝ NHÂN SỰ & PHÂN QUYỀN NỀN TẢNG (PLATFORM IAM)

**Mã tài liệu:** `SPEC-BE-PLATFORM-IAM`  
**Phiên bản:** 1.0  
**Trạng thái:** Ready for Coding (Bổ sung hoàn thiện module IAM cho `BACKEND_PLATFORM_ADMIN_SPEC_v1.0`)  
**Tech Stack:** Go (Golang), Fiber v2, GORM, PostgreSQL 16+, Redis 7+  
**Domain triển khai:** `api.school-erp.vn` (Namespace `/api/v1/platform/iam/*`)

---

## 1. Phạm vi & Nguyên tắc Bảo mật (Security Guardrails)

Tài liệu này hướng dẫn chi tiết cách xây dựng phân hệ **Platform IAM** để `ROOT_ADMIN` (hoặc nhân sự được cấp quyền) quản lý đội ngũ vận hành nền tảng (`platform_users`), tạo các vai trò nội bộ (`platform_roles`) và phân bổ ma trận quyền hạn (`platform_permissions`) ngay trên cổng `admin.school-erp.vn`.

### 1.1 Các quy tắc chống tự khóa & leo thang đặc quyền (Anti-Lockout Rules)

1. **Bảo vệ `ROOT_ADMIN` cuối cùng:** Tuyệt đối không cho phép khóa (`status = 'LOCKED'`), xóa mềm (`deleted_at`), hoặc gỡ vai trò `ROOT_ADMIN` khỏi tài khoản nếu đó là tài khoản `ROOT_ADMIN` có trạng thái `ACTIVE` duy nhất còn lại trong hệ thống.
2. **Cấm tự sửa quyền chính mình (No Self-Escalation):** API cập nhật vai trò (`PUT /users/:id/roles`) hoặc khóa tài khoản (`PATCH /users/:id/status`) bắt buộc từ chối nếu `:id == currentPlatformUser.ID`.
3. **Bảo vệ Role hệ thống (`is_system = true`):** Các vai trò lõi do hệ thống khởi tạo (như `ROOT_ADMIN`) không được phép sửa `code` hoặc xóa (`DELETE`). Các vai trò tùy chỉnh (`is_system = false`) được tạo/sửa/xóa tự do.
4. **Hiệu lực tức thời qua Redis Cache Invalidation:**
   - Khi khóa tài khoản nhân sự $\rightarrow$ Xóa ngay toàn bộ session trên Redis và đưa `jti` vào blacklist để đá văng khỏi hệ thống ngay lập tức.
   - Khi sửa quyền của một Role $\rightarrow$ Truy vấn danh sách tất cả `platform_user_id` đang giữ Role đó và xóa hàng loạt key `platform:authz:user:{user_id}` bằng Redis Pipeline.

---

## 2. Cấu trúc Thư mục Module IAM trong Go

```text
internal/platform/iam/
├── dto.go              # Request/Response structs & validation tags
├── model.go            # GORM models ánh xạ platform_users, platform_roles, platform_permissions
├── repository.go       # Truy vấn DB, đếm số ROOT_ADMIN active, lấy danh sách userID theo roleID
├── service.go          # Logic nghiệp vụ, Transaction GORM, Redis batch invalidation, Audit Log
└── handler.go          # Fiber HTTP Handlers & binding routes
```

---

## 3. Cập nhật Seed Data Quyền IAM (`seed_platform_iam.sql`)

Bổ sung đầy đủ các mã quyền chi tiết cho phân hệ IAM (đảm bảo tách bạch quyền xem, tạo, sửa, gán vai trò và quản lý role) và gán tự động cho `ROOT_ADMIN`:

```sql
-- 1. Bổ sung chi tiết danh mục quyền Platform IAM
INSERT INTO platform_permissions (code, module, name, description) VALUES
('platform:user:read', 'PLATFORM_IAM', 'Xem danh sách nhân sự nền tảng', 'Xem danh sách và chi tiết tài khoản quản trị nền tảng'),
('platform:user:create', 'PLATFORM_IAM', 'Tạo nhân sự nền tảng', 'Khởi tạo tài khoản nhân sự vận hành mới'),
('platform:user:update', 'PLATFORM_IAM', 'Cập nhật & khóa nhân sự nền tảng', 'Sửa thông tin, đặt lại mật khẩu hoặc khóa/mở khóa tài khoản'),
('platform:user:assign_role', 'PLATFORM_IAM', 'Phân vai trò nhân sự nền tảng', 'Gán hoặc thu hồi vai trò của nhân sự nền tảng'),
('platform:role:read', 'PLATFORM_IAM', 'Xem vai trò & danh mục quyền', 'Xem danh sách vai trò và ma trận quyền hạn của nền tảng'),
('platform:role:create', 'PLATFORM_IAM', 'Tạo vai trò nền tảng', 'Tạo vai trò nội bộ mới và gán quyền'),
('platform:role:update', 'PLATFORM_IAM', 'Cập nhật vai trò nền tảng', 'Chỉnh sửa thông tin và ma trận quyền của vai trò'),
('platform:role:delete', 'PLATFORM_IAM', 'Xóa vai trò nền tảng', 'Xóa vai trò tùy chỉnh không còn sử dụng')
ON CONFLICT (LOWER(code)) DO NOTHING;

-- 2. Gán toàn bộ quyền mới vào vai trò ROOT_ADMIN
INSERT INTO platform_role_permissions (platform_role_id, platform_permission_id)
SELECT '00000000-0000-0000-0000-000000000001', id
FROM platform_permissions
WHERE module = 'PLATFORM_IAM'
ON CONFLICT DO NOTHING;
```

---

## 4. Đặc tả Chi tiết API (`/api/v1/platform/iam/*`)

Tất cả endpoint dưới đây đều nằm sau `PlatformAuthMiddleware` (`realm == "platform"`).

### 4.1 Quản lý Nhân sự Nền tảng (`platform_users`)

| Method | Endpoint | Permission yêu cầu | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/platform/iam/users` | `platform:user:read` | Danh sách nhân sự (phân trang `page`, `limit`), filter theo `status` (`ACTIVE`/`LOCKED`), `role_id`, tìm kiếm theo `email` hoặc `full_name`. Kèm mảng `roles` tóm tắt của từng user. |
| `GET` | `/api/v1/platform/iam/users/:id` | `platform:user:read` | Chi tiết nhân sự, trạng thái MFA, thời điểm đăng nhập cuối và danh sách vai trò đang nắm giữ. |
| `POST` | `/api/v1/platform/iam/users` | `platform:user:create` | Tạo tài khoản nhân sự mới + gán danh sách `role_ids` ban đầu trong cùng 1 transaction. Hash mật khẩu bằng Argon2id/Bcrypt. Ghi Audit Log. |
| `PATCH` | `/api/v1/platform/iam/users/:id` | `platform:user:update` | Cập nhật `full_name`, `phone`, `status`. **Guard:** Chặn tự khóa chính mình; chặn khóa `ROOT_ADMIN` active cuối cùng. Nếu chuyển sang `LOCKED`, xóa toàn bộ session trên Redis. |
| `PUT` | `/api/v1/platform/iam/users/:id/roles` | `platform:user:assign_role` | Thay thế toàn bộ danh sách `role_ids` của nhân sự. **Guard:** Chặn tự sửa vai trò chính mình; chặn gỡ `ROOT_ADMIN` khỏi user cuối cùng giữ role này. Xóa cache `platform:authz:user:{id}`. |
| `POST` | `/api/v1/platform/iam/users/:id/reset-password` | `platform:user:update` | Đặt lại mật khẩu tạm thời cho nhân sự, đặt cờ `must_change_password = true` và vô hiệu hóa các phiên đăng nhập cũ. |

#### Mẫu Request & Response `POST /api/v1/platform/iam/users`
```json
// Request
{
  "email": "cskh.hanoi@school-erp.vn",
  "full_name": "Trần Minh Anh",
  "phone": "0912345678",
  "password": "TempPassword@2026",
  "must_change_password": true,
  "role_ids": [
    "22222222-2222-2222-2222-222222222222"
  ]
}

// Response (201 Created)
{
  "success": true,
  "data": {
    "id": "9a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d",
    "email": "cskh.hanoi@school-erp.vn",
    "full_name": "Trần Minh Anh",
    "status": "ACTIVE",
    "must_change_password": true,
    "roles": [
      { "id": "22222222-2222-2222-2222-222222222222", "code": "SUPPORT_AGENT", "name": "Chuyên viên Hỗ trợ" }
    ],
    "created_at": "2026-11-17T09:00:00Z"
  }
}
```

---

### 4.2 Quản lý Vai trò & Ma trận Quyền (`platform_roles` & `platform_permissions`)

| Method | Endpoint | Permission yêu cầu | Mô tả & Logic xử lý |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/platform/iam/permissions` | `platform:role:read` | Lấy toàn bộ `platform_permissions` **được gom nhóm theo `module`** để Frontend vẽ bảng Checkbox Matrix trực quan. |
| `GET` | `/api/v1/platform/iam/roles` | `platform:role:read` | Danh sách `platform_roles` kèm số lượng nhân sự (`user_count`) và số lượng quyền (`permission_count`) của từng vai trò. |
| `GET` | `/api/v1/platform/iam/roles/:id` | `platform:role:read` | Chi tiết một vai trò kèm danh sách `permission_ids` và danh sách nhân sự đang giữ vai trò này. |
| `POST` | `/api/v1/platform/iam/roles` | `platform:role:create` | Tạo vai trò mới (`code` uppercase `^[A-Z0-9_]+$`, `name`, `description`, `is_system = false`) và gán `permission_ids` trong 1 transaction. |
| `PUT` | `/api/v1/platform/iam/roles/:id` | `platform:role:update` | Cập nhật `name`, `description` và đồng bộ lại `permission_ids`. **Bắt buộc xóa cache RBAC trên Redis của toàn bộ user thuộc role này.** |
| `DELETE` | `/api/v1/platform/iam/roles/:id` | `platform:role:delete` | Xóa vai trò. **Guard:** Trả lỗi `403 SYSTEM_ROLE_PROTECTED` nếu `is_system == true`; trả lỗi `409 ROLE_IN_USE` nếu `user_count > 0`. |

#### Mẫu Response `GET /api/v1/platform/iam/permissions` (Gom nhóm cho Ant Design Vue)
```json
{
  "success": true,
  "data": [
    {
      "module": "PLATFORM_IAM",
      "module_label": "Quản trị Nhân sự & Phân quyền Nền tảng",
      "permissions": [
        { "id": "uuid-1", "code": "platform:user:read", "name": "Xem danh sách nhân sự nền tảng" },
        { "id": "uuid-2", "code": "platform:user:create", "name": "Tạo nhân sự nền tảng" }
      ]
    },
    {
      "module": "ORGANIZATION",
      "module_label": "Quản lý Tổ chức & Subdomain",
      "permissions": [
        { "id": "uuid-3", "code": "platform:org:read", "name": "Xem danh sách và chi tiết Tổ chức/Trường" },
        { "id": "uuid-4", "code": "platform:org:create", "name": "Khởi tạo Tổ chức mới" }
      ]
    }
  ]
}
```

---

## 5. Code Mẫu Triển khai Service & Redis Pipeline Invalidation (Go)

### 5.1 Quản lý Redis Active Sessions của một User (`internal/pkg/rediskeys/platform.go`)

Để có thể thu hồi tức thì toàn bộ phiên đăng nhập khi khóa một nhân sự (`status = 'LOCKED'`), khi login bạn nên lưu thêm một Redis `SET` chứa các `jti` đang hoạt động của user đó:

```go
// PlatformUserSessionsKey lưu tập hợp các JTI đang active của một platform_user
func PlatformUserSessionsKey(userID string) string {
	return fmt.Sprintf("platform:user:sessions:%s", userID)
}
```

### 5.2 Service cập nhật quyền Role & Xóa cache hàng loạt (`internal/platform/iam/service.go`)

```go
package iam

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"your-module/internal/pkg/rediskeys"
)

type Service struct {
	db    *gorm.DB
	redis *redis.Client
}

// UpdateRolePermissions cập nhật ma trận quyền cho Role và xóa cache Redis của toàn bộ user giữ Role đó
func (s *Service) UpdateRolePermissions(ctx context.Context, actorID, roleID string, req UpdateRoleRequest) error {
	var affectedUserIDs []string

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var role PlatformRole
		if err := tx.First(&role, "id = ?", roleID).Error; err != nil {
			return err
		}

		// 1. Cập nhật thông tin cơ bản của Role
		if err := tx.Model(&role).Updates(map[string]any{
			"name":        req.Name,
			"description": req.Description,
		}).Error; err != nil {
			return err
		}

		// 2. Xóa quyền cũ và insert danh sách quyền mới
		if err := tx.Where("platform_role_id = ?", roleID).Delete(&PlatformRolePermission{}).Error; err != nil {
			return err
		}

		if len(req.PermissionIDs) > 0 {
			mappings := make([]PlatformRolePermission, 0, len(req.PermissionIDs))
			for _, permID := range req.PermissionIDs {
				mappings = append(mappings, PlatformRolePermission{
					PlatformRoleID:       roleID,
					PlatformPermissionID: permID,
				})
			}
			if err := tx.Create(&mappings).Error; err != nil {
				return err
			}
		}

		// 3. Lấy danh sách toàn bộ user đang giữ Role này để invalidate cache
		if err := tx.Model(&PlatformUserRole{}).
			Where("platform_role_id = ?", roleID).
			Pluck("platform_user_id", &affectedUserIDs).Error; err != nil {
			return err
		}

		// 4. Ghi Audit Log (actor_type = 'PLATFORM')
		// auditService.Log(tx, ...)
		return nil
	})

	if err != nil {
		return err
	}

	// 5. Dùng Redis Pipeline xóa đồng loạt cache quyền của các user bị ảnh hưởng
	if len(affectedUserIDs) > 0 {
		pipe := s.redis.Pipeline()
		for _, uid := range affectedUserIDs {
			pipe.Del(ctx, rediskeys.PlatformUserAuthzKey(uid))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			// Log warning để monitoring cảnh báo, không làm fail request đã commit DB
			fmt.Printf("[WARN] failed to invalidate platform user authz cache: %v\n", err)
		}
	}

	return nil
}

// LockUser khóa tài khoản nhân sự, kiểm tra anti-lockout và đá văng toàn bộ session trên Redis
func (s *Service) LockUser(ctx context.Context, actorID, targetUserID string) error {
	if actorID == targetUserID {
		return errors.New("SELF_LOCK_NOT_ALLOWED")
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Kiểm tra xem targetUserID có phải là ROOT_ADMIN active cuối cùng không
		var activeRootCount int64
		tx.Table("platform_users u").
			Joins("JOIN platform_user_roles ur ON ur.platform_user_id = u.id").
			Joins("JOIN platform_roles r ON r.id = ur.platform_role_id").
			Where("r.code = 'ROOT_ADMIN' AND u.status = 'ACTIVE' AND u.deleted_at IS NULL AND u.id != ?", targetUserID).
			Count(&activeRootCount)

		if activeRootCount == 0 {
			return errors.New("LAST_ROOT_ADMIN_PROTECTED")
		}

		return tx.Model(&PlatformUser{}).
			Where("id = ?", targetUserID).
			Update("status", "LOCKED").Error
	})

	if err != nil {
		return err
	}

	// Thu hồi toàn bộ session đang mở của user trên Redis
	userSessionsKey := rediskeys.PlatformUserSessionsKey(targetUserID)
	jtis, _ := s.redis.SMembers(ctx, userSessionsKey).Result()

	pipe := s.redis.Pipeline()
	for _, jti := range jtis {
		pipe.Del(ctx, rediskeys.PlatformSessionKey(jti))
		pipe.Set(ctx, rediskeys.PlatformBlacklistJTIKey(jti), "1", rediskeys.TTLPlatformSession)
	}
	pipe.Del(ctx, userSessionsKey)
	pipe.Del(ctx, rediskeys.PlatformUserAuthzKey(targetUserID))
	_, _ = pipe.Exec(ctx)

	return nil
}
```

---

## 6. Danh mục Mã lỗi Bổ sung cho Module IAM

| HTTP Status | Error Code | Ý nghĩa |
| :--- | :--- | :--- |
| `403` | `SELF_MODIFICATION_NOT_ALLOWED` | Nhân sự cố tình tự khóa tài khoản hoặc tự đổi vai trò của chính mình |
| `403` | `SYSTEM_ROLE_PROTECTED` | Cố tình xóa hoặc đổi `code` của vai trò hệ thống (`is_system = true`) |
| `409` | `PLATFORM_EMAIL_ALREADY_EXISTS` | Email nhân sự nền tảng đã tồn tại trong bảng `platform_users` |
| `409` | `ROLE_IN_USE` | Không thể xóa vai trò vì đang có nhân sự nắm giữ vai trò này |
| `422` | `LAST_ROOT_ADMIN_PROTECTED` | Vi phạm quy tắc bảo vệ tài khoản `ROOT_ADMIN` hoạt động cuối cùng |

---

## 7. Checklist Triển khai Nhanh

1. [ ] Chạy script `seed_platform_iam.sql` để bổ sung đầy đủ 8 mã quyền nhóm `PLATFORM_IAM`.
2. [ ] Cập nhật luồng `Login` và `Logout` lưu/xóa thêm `jti` vào set `platform:user:sessions:{userID}` để hỗ trợ khóa tài khoản tức thời.
3. [ ] Triển khai package `internal/platform/iam` (Repository, Service, Handler) và đăng ký vào router `/api/v1/platform/iam/*`.
4. [ ] Kiểm thử 3 kịch bản bảo mật: tự khóa chính mình (`403`), khóa `ROOT_ADMIN` cuối cùng (`422`), và cập nhật quyền của một Role làm mất quyền ngay lập tức của nhân sự đang online.