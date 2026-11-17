DELETE FROM platform_permissions
WHERE code IN ('platform:media:read', 'platform:media:manage');

DROP TABLE IF EXISTS stored_files;
DROP TABLE IF EXISTS organization_storage_usages;
