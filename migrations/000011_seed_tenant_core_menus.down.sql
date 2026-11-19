DELETE FROM menus
WHERE menu_key IN (
    'tenant-org-menus',
    'tenant-iam-roles',
    'tenant-iam-members',
    'tenant-schools',
    'tenant-org-settings',
    'tenant-admin'
);

DELETE FROM tenant_routes
WHERE route_key IN (
    'org.settings',
    'org.schools',
    'org.menus',
    'iam.members',
    'iam.roles'
);
