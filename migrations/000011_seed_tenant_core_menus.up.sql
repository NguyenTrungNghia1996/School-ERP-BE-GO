-- Seed Core Tenant Routes & Base Menus cho giao diện Quản trị Đơn vị / Trường học (CORE_ORG, CORE_IAM)

INSERT INTO tenant_routes (id, route_key, path, feature_id, name, is_active) VALUES
('770e8400-e29b-41d4-a716-446655440010', 'org.settings', '/settings/organization', (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'Thông tin Đơn vị', TRUE),
('770e8400-e29b-41d4-a716-446655440011', 'org.schools', '/settings/schools', (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'Danh sách Trường học', TRUE),
('770e8400-e29b-41d4-a716-446655440012', 'org.menus', '/settings/menus', (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'Tùy chỉnh Menu', TRUE),
('770e8400-e29b-41d4-a716-446655440013', 'iam.members', '/iam/members', (SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), 'Nhân sự & Thành viên', TRUE),
('770e8400-e29b-41d4-a716-446655440014', 'iam.roles', '/iam/roles', (SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), 'Vai trò & Phân quyền', TRUE)
ON CONFLICT DO NOTHING;

INSERT INTO menus (id, parent_id, menu_key, label, icon, menu_type, route_id, feature_id, required_permissions, permission_match_mode, target_portals, is_mandatory, sort_order, is_active) VALUES
('880e8400-e29b-41d4-a716-446655440010', NULL, 'tenant-admin', 'Quản trị Hệ thống', 'SettingOutlined', 'SUBMENU', NULL, (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), '[]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 90, TRUE),
('880e8400-e29b-41d4-a716-446655440011', '880e8400-e29b-41d4-a716-446655440010', 'tenant-org-settings', 'Thông tin Đơn vị', 'BankOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440010', (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), '["org:settings:update"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 1, TRUE),
('880e8400-e29b-41d4-a716-446655440012', '880e8400-e29b-41d4-a716-446655440010', 'tenant-schools', 'Danh sách Trường học', 'ApartmentOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440011', (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), '["school:read"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 2, TRUE),
('880e8400-e29b-41d4-a716-446655440013', '880e8400-e29b-41d4-a716-446655440010', 'tenant-iam-members', 'Nhân sự & Thành viên', 'TeamOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440013', (SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), '["iam:member:read"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 3, TRUE),
('880e8400-e29b-41d4-a716-446655440014', '880e8400-e29b-41d4-a716-446655440010', 'tenant-iam-roles', 'Vai trò & Phân quyền', 'SafetyCertificateOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440014', (SELECT id FROM features WHERE UPPER(code) = 'CORE_IAM'), '["iam:role:read","iam:role:manage"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 4, TRUE),
('880e8400-e29b-41d4-a716-446655440015', '880e8400-e29b-41d4-a716-446655440010', 'tenant-org-menus', 'Tùy chỉnh Menu', 'MenuOutlined', 'ITEM', '770e8400-e29b-41d4-a716-446655440012', (SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), '["org:menu:manage"]'::jsonb, 'ANY', '["SCHOOL_ADMIN"]'::jsonb, FALSE, 5, TRUE)
ON CONFLICT (menu_key) DO NOTHING;
