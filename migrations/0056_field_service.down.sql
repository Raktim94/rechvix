DROP FUNCTION IF EXISTS maintenance_due_organisations();
DROP TABLE IF EXISTS maintenance_plans;
DROP TABLE IF EXISTS incident_updates;
DROP TABLE IF EXISTS incidents;
DROP TABLE IF EXISTS incident_counters;
DROP TABLE IF EXISTS customer_assets;
DELETE FROM role_permissions WHERE permission_code IN ('fieldservice.view', 'fieldservice.manage');
DELETE FROM permissions WHERE code IN ('fieldservice.view', 'fieldservice.manage');
