DELETE FROM platform_permissions
WHERE code IN (
    'platform:user:create',
    'platform:user:update',
    'platform:user:assign_role',
    'platform:role:read',
    'platform:role:create',
    'platform:role:update',
    'platform:role:delete'
);
