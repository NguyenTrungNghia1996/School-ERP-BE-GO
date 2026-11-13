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
ON CONFLICT (keyword) DO NOTHING;

-- 5. Seed Cây Menu mặc định cho Cổng Platform Admin (Ant Design Vue)
INSERT INTO platform_menus (id, parent_id, menu_key, label, icon, route_path, menu_type, required_permission_code, badge_key, sort_order, is_hidden, is_active) VALUES
('10000000-0000-0000-0000-000000000001', NULL, 'dashboard', 'Tổng quan', 'DashboardOutlined', '/dashboard', 'ITEM', NULL, NULL, 1, FALSE, TRUE),
('10000000-0000-0000-0000-000000000002', NULL, 'tenant-management', 'Khách hàng & Tổ chức', 'BankOutlined', NULL, 'SUBMENU', NULL, NULL, 2, FALSE, TRUE),
('10000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000002', 'org-list', 'Danh sách Tổ chức', 'ApartmentOutlined', '/organizations', 'ITEM', 'platform:org:read', 'pending_orgs', 1, FALSE, TRUE),
('10000000-0000-0000-0000-000000000004', '10000000-0000-0000-0000-000000000002', 'reserved-subdomains', 'Subdomain giữ chỗ', 'GlobalOutlined', '/reserved-subdomains', 'ITEM', 'platform:subdomain:manage', NULL, 2, FALSE, TRUE),
('10000000-0000-0000-0000-000000000005', NULL, 'catalog-billing', 'Danh mục & Gói cước', 'AppstoreOutlined', NULL, 'SUBMENU', NULL, NULL, 3, FALSE, TRUE),
('10000000-0000-0000-0000-000000000006', '10000000-0000-0000-0000-000000000005', 'catalog-features', 'Danh mục Tính năng', 'ClusterOutlined', '/catalog/features', 'ITEM', 'platform:feature:manage', NULL, 1, FALSE, TRUE),
('10000000-0000-0000-0000-000000000007', '10000000-0000-0000-0000-000000000005', 'catalog-permissions', 'Permission Catalog', 'SafetyCertificateOutlined', '/catalog/permissions', 'ITEM', 'platform:permission:manage', NULL, 2, FALSE, TRUE),
('10000000-0000-0000-0000-000000000008', '10000000-0000-0000-0000-000000000005', 'catalog-menus', 'Menu nền Tenant', 'MenuOutlined', '/catalog/menus', 'ITEM', 'platform:menu:manage', NULL, 3, FALSE, TRUE),
('10000000-0000-0000-0000-000000000009', '10000000-0000-0000-0000-000000000005', 'service-packages', 'Gói dịch vụ & Add-on', 'CodeSandboxOutlined', '/packages', 'ITEM', 'platform:package:read', NULL, 4, FALSE, TRUE),
('10000000-0000-0000-0000-000000000010', NULL, 'support-sessions', 'Phiên hỗ trợ (Support Mode)', 'CustomerServiceOutlined', '/support-sessions', 'ITEM', 'platform:support:request', NULL, 4, FALSE, TRUE),
('10000000-0000-0000-0000-000000000011', NULL, 'platform-iam', 'Nhân sự & Phân quyền', 'TeamOutlined', NULL, 'SUBMENU', NULL, NULL, 5, FALSE, TRUE),
('10000000-0000-0000-0000-000000000012', '10000000-0000-0000-0000-000000000011', 'platform-users', 'Nhân sự nền tảng', 'UserOutlined', '/iam/users', 'ITEM', 'platform:user:read', NULL, 1, FALSE, TRUE),
('10000000-0000-0000-0000-000000000013', '10000000-0000-0000-0000-000000000011', 'platform-roles', 'Vai trò & Quyền hạn', 'KeyOutlined', '/iam/roles', 'ITEM', 'platform:user:read', NULL, 2, FALSE, TRUE),
('10000000-0000-0000-0000-000000000014', NULL, 'audit-logs', 'Nhật ký Kiểm toán', 'FileSearchOutlined', '/audit-logs', 'ITEM', 'platform:audit:read', NULL, 6, FALSE, TRUE)
ON CONFLICT (menu_key) DO NOTHING;
