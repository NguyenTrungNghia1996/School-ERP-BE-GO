-- Seed quyền quản lý ảnh & dung lượng lưu trữ cho Tenant (thuộc feature CORE_ORG)
INSERT INTO permissions (feature_id, code, resource, action, name, description, is_sensitive) VALUES
((SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'org:media:read', 'org_media', 'read', 'Xem thư viện ảnh và dung lượng đơn vị', 'Xem danh sách ảnh đã tải lên và thống kê dung lượng lưu trữ của đơn vị', FALSE),
((SELECT id FROM features WHERE UPPER(code) = 'CORE_ORG'), 'org:media:manage', 'org_media', 'manage', 'Tải lên và xóa ảnh của đơn vị', 'Upload ảnh vào thư mục đơn vị/trường học và xóa tệp ảnh', FALSE)
ON CONFLICT (LOWER(code)) DO NOTHING;

-- Tự động cấp 2 quyền media cho toàn bộ vai trò hệ thống SCHOOL_ADMIN hiện có
INSERT INTO role_permissions (organization_id, role_id, permission_id)
SELECT r.organization_id, r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE UPPER(r.code) = 'SCHOOL_ADMIN'
  AND r.is_system = TRUE
  AND LOWER(p.code) IN ('org:media:read', 'org:media:manage')
ON CONFLICT DO NOTHING;
