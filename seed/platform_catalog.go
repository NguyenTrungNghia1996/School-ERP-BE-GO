package seed

import (
	"context"
	"log"
	"school-erp/internal/pkg/rediskeys"
	"school-erp/internal/platform/iam"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// EnsurePlatformPermissionsAndMenus idempotently upserts all platform permissions, assigns them to ROOT_ADMIN,
// and upserts the platform sidebar/profile menus (including platform-media and platform-profile).
func EnsurePlatformPermissionsAndMenus(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// 1. Đảm bảo vai trò hệ thống ROOT_ADMIN tồn tại
		if err := tx.Exec(`
			INSERT INTO platform_roles (id, code, name, description, is_system)
			VALUES (?, 'ROOT_ADMIN', 'Quản trị viên Tối cao', 'Toàn quyền quản trị nền tảng SaaS', TRUE)
			ON CONFLICT DO NOTHING
		`, iam.RootAdminRoleID).Error; err != nil {
			return err
		}

		// 2. Upsert toàn bộ quyền nền tảng khớp 100% với middleware trong internal/platform/router.go
		if err := tx.Exec(`
			INSERT INTO platform_permissions (code, module, name, description) VALUES
			('platform:user:read', 'PLATFORM_IAM', 'Xem danh sách nhân sự nền tảng', 'Xem danh sách và chi tiết tài khoản quản trị nền tảng'),
			('platform:user:manage', 'PLATFORM_IAM', 'Quản lý tài khoản nhân sự nền tảng', 'Toàn quyền quản lý nhân sự nền tảng (alias legacy)'),
			('platform:user:create', 'PLATFORM_IAM', 'Tạo nhân sự nền tảng', 'Khởi tạo tài khoản nhân sự vận hành mới'),
			('platform:user:update', 'PLATFORM_IAM', 'Cập nhật, khóa, tắt MFA & xóa mềm nhân sự nền tảng', 'Sửa thông tin, đặt lại mật khẩu, tắt MFA, khóa hoặc xóa mềm tài khoản'),
			('platform:user:assign_role', 'PLATFORM_IAM', 'Phân vai trò nhân sự nền tảng', 'Gán hoặc thu hồi vai trò của nhân sự nền tảng'),
			('platform:role:read', 'PLATFORM_IAM', 'Xem vai trò và ma trận quyền nền tảng', 'Xem danh sách, chi tiết vai trò và ma trận quyền theo module'),
			('platform:role:manage', 'PLATFORM_IAM', 'Quản lý vai trò và phân quyền nền tảng', 'Toàn quyền quản lý vai trò nền tảng (alias legacy)'),
			('platform:role:create', 'PLATFORM_IAM', 'Tạo vai trò nền tảng', 'Tạo vai trò tùy chỉnh và gán quyền'),
			('platform:role:update', 'PLATFORM_IAM', 'Cập nhật vai trò nền tảng', 'Sửa thông tin và ma trận quyền của vai trò'),
			('platform:role:delete', 'PLATFORM_IAM', 'Xóa vai trò nền tảng', 'Xóa vai trò tùy chỉnh không còn người dùng'),
			('platform:org:read', 'ORGANIZATION', 'Xem danh sách Tổ chức và Trường trực thuộc', 'Xem danh sách, chi tiết Tổ chức và danh sách trường học trực thuộc'),
			('platform:org:create', 'ORGANIZATION', 'Khởi tạo và cập nhật Tổ chức', 'Tạo mới và cập nhật thông tin, logo Tổ chức'),
			('platform:org:approve', 'ORGANIZATION', 'Phê duyệt Tổ chức đăng ký', 'Phê duyệt Tổ chức đang chờ kích hoạt'),
			('platform:org:status', 'ORGANIZATION', 'Khóa, mở khóa và xóa mềm Tổ chức', 'Cập nhật trạng thái hoạt động hoặc xóa mềm Tổ chức'),
			('platform:subdomain:manage', 'ORGANIZATION', 'Quản lý subdomain giữ chỗ, đồng bộ Cloudflare và đổi subdomain', 'Quản lý reserved_subdomains, sync Cloudflare DNS và đổi subdomain'),
			('platform:media:read', 'MEDIA', 'Xem thư viện Media và dung lượng lưu trữ', 'Xem danh sách tệp, thùng rác, URL truy cập và thống kê dung lượng R2/S3'),
			('platform:media:manage', 'MEDIA', 'Upload, khôi phục và xóa tệp Media', 'Upload, xóa mềm/vĩnh viễn và khôi phục tệp cấp Platform và Tổ chức'),
			('platform:feature:manage', 'CATALOG', 'Quản lý danh mục tính năng (Features)', 'Tạo, sửa và xóa tính năng trong Catalog'),
			('platform:permission:manage', 'CATALOG', 'Quản lý danh mục quyền Tenant (Permissions)', 'Tạo, sửa và xóa quyền Tenant trong Catalog'),
			('platform:menu:manage', 'CATALOG', 'Quản lý cây menu nền và Routes cho Tenant', 'Tạo, sửa, sắp xếp, xóa menu nền và CRUD Tenant Routes'),
			('platform:package:read', 'PACKAGE', 'Xem danh sách gói dịch vụ và Add-on', 'Xem gói dịch vụ và chi tiết các phiên bản gói'),
			('platform:package:manage', 'PACKAGE', 'Quản lý gói dịch vụ và phiên bản gói', 'Tạo, sửa, xóa gói dịch vụ; tạo, sửa, phát hành, hủy phát hành và xóa phiên bản DRAFT'),
			('platform:subscription:manage', 'PACKAGE', 'Cấp, gia hạn và thu hồi gói dịch vụ của Tổ chức', 'Gán gói, đổi gói chính, gia hạn, tạm ngưng hoặc thu hồi subscription'),
			('platform:support:request', 'SUPPORT', 'Tạo yêu cầu phiên hỗ trợ vào trường', 'Tạo và đổi token phiên Support Mode'),
			('platform:support:approve', 'SUPPORT', 'Phê duyệt phiên hỗ trợ', 'Phê duyệt yêu cầu phiên Support Mode'),
			('platform:support:revoke', 'SUPPORT', 'Thu hồi phiên hỗ trợ đang hoạt động', 'Ngắt sớm phiên Support Mode'),
			('platform:audit:read', 'AUDIT', 'Tra cứu nhật ký kiểm toán hệ thống', 'Xem lịch sử thao tác kiểm toán trên toàn nền tảng')
			ON CONFLICT (LOWER(code)) DO UPDATE SET
				module = EXCLUDED.module,
				name = EXCLUDED.name,
				description = EXCLUDED.description
		`).Error; err != nil {
			return err
		}

		// 3. Gán toàn bộ quyền nền tảng cho ROOT_ADMIN
		if err := tx.Exec(`
			INSERT INTO platform_role_permissions (platform_role_id, platform_permission_id)
			SELECT ?, id FROM platform_permissions
			ON CONFLICT DO NOTHING
		`, iam.RootAdminRoleID).Error; err != nil {
			return err
		}

		// 4. Upsert bổ sung các node menu nền tảng mới (giữ nguyên 14 node hiện có)
		if err := tx.Exec(`
			INSERT INTO platform_menus (id, parent_id, menu_key, label, icon, route_path, menu_type, required_permission_code, badge_key, sort_order, is_hidden, is_active) VALUES
			('10000000-0000-0000-0000-000000000015', NULL, 'platform-media', 'Thư viện Media', 'CloudServerOutlined', '/profile?tab=media', 'ITEM', 'platform:media:read', NULL, 7, FALSE, TRUE),
			('10000000-0000-0000-0000-000000000016', NULL, 'platform-profile', 'Hồ sơ cá nhân', 'UserOutlined', '/profile', 'ITEM', NULL, NULL, 99, TRUE, TRUE)
			ON CONFLICT (menu_key) DO UPDATE SET
				label = EXCLUDED.label,
				icon = EXCLUDED.icon,
				route_path = EXCLUDED.route_path,
				menu_type = EXCLUDED.menu_type,
				required_permission_code = EXCLUDED.required_permission_code,
				sort_order = EXCLUDED.sort_order,
				is_hidden = EXCLUDED.is_hidden,
				is_active = EXCLUDED.is_active,
				updated_at = NOW()
		`).Error; err != nil {
			return err
		}

		return nil
	})
}

// EnsureTenantCatalog idempotently upserts core Tenant Catalog features, permissions, routes, and base menus
// (including CORE_MEDIA + media.library + tenant_media and Organization/School/IAM core routes & menus).
func EnsureTenantCatalog(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// 1. Features lõi của Tenant
		if err := tx.Exec(`
			INSERT INTO features (id, code, module, name, description, is_core, is_active) VALUES
			('550e8400-e29b-41d4-a716-446655440010', 'CORE_ORG', 'ORGANIZATION', 'Quản lý Đơn vị & Trường học', 'Cài đặt thông tin tổ chức, danh sách trường trực thuộc và menu đơn vị', TRUE, TRUE),
			('550e8400-e29b-41d4-a716-446655440011', 'CORE_IAM', 'IAM', 'Quản lý Nhân sự & Phân quyền Đơn vị', 'Quản lý thành viên trường học, vai trò tùy chỉnh và phân quyền RBAC', TRUE, TRUE),
			('550e8400-e29b-41d4-a716-446655440012', 'CORE_MEDIA', 'MEDIA', 'Thư viện Media & Lưu trữ', 'Quản lý ảnh, tài liệu (PDF, Word, Excel), thùng rác và hạn mức dung lượng đơn vị', TRUE, TRUE)
			ON CONFLICT (UPPER(code)) DO UPDATE SET
				module = EXCLUDED.module,
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				is_core = EXCLUDED.is_core,
				is_active = TRUE,
				updated_at = NOW()
		`).Error; err != nil {
			return err
		}

		// 2. Permissions lõi của Tenant (bao gồm org:media:* và media:view/upload/delete)
		if err := tx.Exec(`
			INSERT INTO permissions (feature_id, code, resource, action, name, description, is_sensitive, is_active) VALUES
			((SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'org:media:read', 'org_media', 'read', 'Xem thư viện ảnh và dung lượng đơn vị', 'Xem danh sách tệp, thùng rác và thống kê dung lượng lưu trữ của đơn vị', FALSE, TRUE),
			((SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'org:media:manage', 'org_media', 'manage', 'Tải lên, khôi phục và xóa tệp của đơn vị', 'Upload, khôi phục và xóa tệp media trong thư mục đơn vị/trường học', FALSE, TRUE),
			((SELECT id FROM features WHERE UPPER(code) = 'CORE_MEDIA'), 'media:view', 'media', 'view', 'Xem thư viện Media đơn vị', 'Xem danh sách tệp media, thùng rác và hạn mức dung lượng lưu trữ', FALSE, TRUE),
			((SELECT id FROM features WHERE UPPER(code) = 'CORE_MEDIA'), 'media:upload', 'media', 'upload', 'Tải lên tệp Media đơn vị', 'Tải lên ảnh và tài liệu PDF, Word, Excel vào thư viện đơn vị', FALSE, TRUE),
			((SELECT id FROM features WHERE UPPER(code) = 'CORE_MEDIA'), 'media:delete', 'media', 'delete', 'Xóa và khôi phục tệp Media đơn vị', 'Chuyển vào thùng rác, khôi phục hoặc xóa vĩnh viễn tệp Media', FALSE, TRUE)
			ON CONFLICT (LOWER(code)) DO UPDATE SET
				feature_id = EXCLUDED.feature_id,
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				is_active = TRUE,
				updated_at = NOW()
		`).Error; err != nil {
			return err
		}

		// 3. Gắn các feature lõi vào gói PRIMARY mẫu (nếu tồn tại)
		if err := tx.Exec(`
			INSERT INTO service_package_features (package_version_id, feature_id)
			SELECT '770e8400-e29b-41d4-a716-446655440098', id
			FROM features
			WHERE UPPER(code) IN ('CORE_ORG', 'CORE_IAM', 'CORE_MEDIA')
			  AND EXISTS (SELECT 1 FROM service_package_versions WHERE id = '770e8400-e29b-41d4-a716-446655440098')
			ON CONFLICT DO NOTHING
		`).Error; err != nil {
			return err
		}

		// 4. Tự động cấp các quyền lõi (CORE_ORG, CORE_IAM, CORE_MEDIA) cho toàn bộ vai trò SCHOOL_ADMIN hiện có
		if err := tx.Exec(`
			INSERT INTO role_permissions (organization_id, role_id, permission_id)
			SELECT r.organization_id, r.id, p.id
			FROM roles r
			CROSS JOIN permissions p
			JOIN features f ON f.id = p.feature_id
			WHERE UPPER(r.code) = 'SCHOOL_ADMIN'
			  AND r.is_system = TRUE
			  AND p.is_active = TRUE
			  AND UPPER(f.code) IN ('CORE_ORG', 'CORE_IAM', 'CORE_MEDIA')
			ON CONFLICT DO NOTHING
		`).Error; err != nil {
			return err
		}

		// 5. Upsert Tenant Routes nền (Tổ chức, Trường học, Menu, IAM, Media)
		if err := tx.Exec(`
			INSERT INTO tenant_routes (id, route_key, path, feature_id, name, is_active) VALUES
			('770e8400-e29b-41d4-a716-446655440010', 'org.settings', '/settings/organization', (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'Thông tin Đơn vị', TRUE),
			('770e8400-e29b-41d4-a716-446655440011', 'org.schools', '/settings/schools', (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'Danh sách Trường học', TRUE),
			('770e8400-e29b-41d4-a716-446655440012', 'org.menus', '/settings/menus', (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'Tùy chỉnh Menu', TRUE),
			('770e8400-e29b-41d4-a716-446655440013', 'iam.members', '/iam/members', (SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), 'Nhân sự & Thành viên', TRUE),
			('770e8400-e29b-41d4-a716-446655440014', 'iam.roles', '/iam/roles', (SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), 'Vai trò & Phân quyền', TRUE),
			('770e8400-e29b-41d4-a716-446655440015', 'media.library', '/media', (SELECT id FROM features WHERE UPPER(code) = 'CORE_MEDIA'), 'Thư viện Media', TRUE)
			ON CONFLICT (route_key) DO UPDATE SET
				path = EXCLUDED.path,
				feature_id = EXCLUDED.feature_id,
				name = EXCLUDED.name,
				is_active = TRUE
		`).Error; err != nil {
			return err
		}

		// 6. Upsert Tenant Base Menus nền (bao gồm tenant_media)
		if err := tx.Exec(`
			INSERT INTO menus (id, parent_id, menu_key, label, icon, menu_type, route_id, feature_id, required_permissions, permission_match_mode, target_portals, is_mandatory, sort_order, is_active) VALUES
			('880e8400-e29b-41d4-a716-446655440010', NULL, 'tenant-admin', 'Quản trị Hệ thống', 'SettingOutlined', 'SUBMENU', NULL, (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), '[]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 90, TRUE),
			('880e8400-e29b-41d4-a716-446655440011', '880e8400-e29b-41d4-a716-446655440010', 'tenant-org-settings', 'Thông tin Đơn vị', 'BankOutlined', 'ITEM', (SELECT id FROM tenant_routes WHERE route_key = 'org.settings'), (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), '["org:settings:update"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 1, TRUE),
			('880e8400-e29b-41d4-a716-446655440012', '880e8400-e29b-41d4-a716-446655440010', 'tenant-schools', 'Danh sách Trường học', 'ApartmentOutlined', 'ITEM', (SELECT id FROM tenant_routes WHERE route_key = 'org.schools'), (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), '["school:read"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 2, TRUE),
			('880e8400-e29b-41d4-a716-446655440013', '880e8400-e29b-41d4-a716-446655440010', 'tenant-iam-members', 'Nhân sự & Thành viên', 'TeamOutlined', 'ITEM', (SELECT id FROM tenant_routes WHERE route_key = 'iam.members'), (SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), '["iam:member:read"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 3, TRUE),
			('880e8400-e29b-41d4-a716-446655440014', '880e8400-e29b-41d4-a716-446655440010', 'tenant-iam-roles', 'Vai trò & Phân quyền', 'SafetyCertificateOutlined', 'ITEM', (SELECT id FROM tenant_routes WHERE route_key = 'iam.roles'), (SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), '["iam:role:read","iam:role:manage"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 4, TRUE),
			('880e8400-e29b-41d4-a716-446655440015', '880e8400-e29b-41d4-a716-446655440010', 'tenant-org-menus', 'Tùy chỉnh Menu', 'MenuOutlined', 'ITEM', (SELECT id FROM tenant_routes WHERE route_key = 'org.menus'), (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), '["org:menu:manage"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 5, TRUE),
			('880e8400-e29b-41d4-a716-446655440016', '880e8400-e29b-41d4-a716-446655440010', 'tenant_media', 'Thư viện Media', 'CloudServerOutlined', 'ITEM', (SELECT id FROM tenant_routes WHERE route_key = 'media.library'), (SELECT id FROM features WHERE UPPER(code) = 'CORE_MEDIA'), '["org:media:read","media:view"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 6, TRUE)
			ON CONFLICT (menu_key) DO UPDATE SET
				parent_id = EXCLUDED.parent_id,
				label = EXCLUDED.label,
				icon = EXCLUDED.icon,
				menu_type = EXCLUDED.menu_type,
				route_id = EXCLUDED.route_id,
				feature_id = EXCLUDED.feature_id,
				required_permissions = EXCLUDED.required_permissions,
				permission_match_mode = EXCLUDED.permission_match_mode,
				target_portals = EXCLUDED.target_portals,
				sort_order = EXCLUDED.sort_order,
				is_active = TRUE,
				updated_at = NOW()
		`).Error; err != nil {
			return err
		}

		return nil
	})
}

// InvalidateCaches clears cached platform/tenant menus, authz permissions, and entitlements on Redis
// so newly seeded menus & permissions take effect immediately.
func InvalidateCaches(rdb *redis.Client) {
	if rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = rdb.Del(ctx, rediskeys.PlatformMenuTreeKey()).Err()

	patterns := []string{
		"platform:authz:user:*",
		"tenant:authz:member:*",
		"tenant:entitlement:*",
		"tenant:menus:*",
	}
	var deleted int
	for _, pattern := range patterns {
		iter := rdb.Scan(ctx, 0, pattern, 200).Iterator()
		for iter.Next(ctx) {
			if err := rdb.Del(ctx, iter.Val()).Err(); err == nil {
				deleted++
			}
		}
	}
	log.Printf("🧹 Đã làm mới cache Redis sau khi seed (xóa platform:menu:tree và %d key phân quyền/menu)\n", deleted)
}
