DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS staff_attendance;
DROP TABLE IF EXISTS staff_members;

DELETE FROM role_permissions WHERE permission_code IN ('staff.view', 'staff.manage');
DELETE FROM permissions WHERE code IN ('staff.view', 'staff.manage');
