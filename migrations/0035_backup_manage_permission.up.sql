-- Backup/restore (Stage 16): a distinct permission from settings.manage
-- on purpose — every Owner-equivalent user already holds settings.manage
-- for day-to-day business configuration, but exporting a full,
-- decryptable copy of every organisation's data or restoring over the
-- live database is a meaningfully more sensitive action than "edit
-- invoice branding." Keeping it a separate permission code means a
-- future, more granular role can grant one without the other, even
-- though today's only role (Owner) always holds every permission
-- (Stage 2).
INSERT INTO permissions (code, module, description) VALUES
    ('backup.manage', 'backup', 'Export and restore full database backups');

-- Same "system roles ship with a fixed starter permission set" backfill
-- migrations/0033 already established — without this, every organisation
-- bootstrapped before this migration would have an Owner role that can
-- never grant itself the one permission this feature needs.
INSERT INTO role_permissions (role_id, permission_code)
SELECT roles.id, 'backup.manage'
FROM roles
WHERE roles.is_system = true
ON CONFLICT DO NOTHING;
