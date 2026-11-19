DELETE FROM role_permissions
WHERE permission_id IN (
    SELECT id FROM permissions WHERE LOWER(code) IN ('org:media:read', 'org:media:manage')
);

DELETE FROM permissions
WHERE LOWER(code) IN ('org:media:read', 'org:media:manage');
