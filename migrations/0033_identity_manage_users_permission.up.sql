-- Team member management (Stage 12): letting an Owner add additional
-- logins to their own organisation. Bootstrap (Stage 2) only ever
-- creates the single owner user — until now there was no way for a
-- second person at the same business to get a login at all.
INSERT INTO permissions (code, module, description) VALUES
    ('identity.view_users',   'identity', 'View team members in the organisation'),
    ('identity.manage_users', 'identity', 'Create user accounts for the organisation');

-- Same "system roles ship with a fixed starter permission set" contract
-- roles.is_system already documents (migrations/0002_rbac_catalog.up.sql)
-- — without this backfill, every organisation bootstrapped before this
-- migration would have an Owner role that can never grant itself the one
-- permission needed to reach the feature this migration exists for.
INSERT INTO role_permissions (role_id, permission_code)
SELECT roles.id, p.code
FROM roles, (VALUES ('identity.view_users'), ('identity.manage_users')) AS p(code)
WHERE roles.is_system = true
ON CONFLICT DO NOTHING;
