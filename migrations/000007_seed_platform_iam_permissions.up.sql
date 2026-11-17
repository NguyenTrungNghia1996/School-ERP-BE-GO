-- 1. Bổ sung chi tiết danh mục quyền Platform IAM (theo SPEC-BE-PLATFORM-IAM v1.0)
INSERT INTO platform_permissions (code, module, name, description) VALUES
('platform:user:read', 'PLATFORM_IAM', 'Xem danh sách nhân sự nền tảng', 'Xem danh sách và chi tiết tài khoản quản trị nền tảng'),
('platform:user:create', 'PLATFORM_IAM', 'Tạo nhân sự nền tảng', 'Khởi tạo tài khoản nhân sự vận hành mới'),
('platform:user:update', 'PLATFORM_IAM', 'Cập nhật & khóa nhân sự nền tảng', 'Sửa thông tin, đặt lại mật khẩu hoặc khóa/mở khóa tài khoản'),
('platform:user:assign_role', 'PLATFORM_IAM', 'Phân vai trò nhân sự nền tảng', 'Gán hoặc thu hồi vai trò của nhân sự nền tảng'),
('platform:role:read', 'PLATFORM_IAM', 'Xem vai trò & danh mục quyền', 'Xem danh sách vai trò và ma trận quyền hạn của nền tảng'),
('platform:role:create', 'PLATFORM_IAM', 'Tạo vai trò nền tảng', 'Tạo vai trò nội bộ mới và gán quyền'),
('platform:role:update', 'PLATFORM_IAM', 'Cập nhật vai trò nền tảng', 'Chỉnh sửa thông tin và ma trận quyền của vai trò'),
('platform:role:delete', 'PLATFORM_IAM', 'Xóa vai trò nền tảng', 'Xóa vai trò tùy chỉnh không còn sử dụng')
ON CONFLICT (LOWER(code)) DO UPDATE
SET name = EXCLUDED.name,
    description = EXCLUDED.description;

-- 2. Gán toàn bộ quyền nhóm PLATFORM_IAM vào vai trò ROOT_ADMIN
INSERT INTO platform_role_permissions (platform_role_id, platform_permission_id)
SELECT '00000000-0000-0000-0000-000000000001', id
FROM platform_permissions
WHERE module = 'PLATFORM_IAM'
ON CONFLICT DO NOTHING;
