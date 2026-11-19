DROP TABLE IF EXISTS organization_menu_configs;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS roles;

ALTER TABLE school_memberships DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_school_memberships ON school_memberships;
ALTER TABLE schools DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_schools ON schools;
