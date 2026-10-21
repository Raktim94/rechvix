DROP FUNCTION IF EXISTS reminder_organisations();
DROP TABLE IF EXISTS reminder_log;
DROP TABLE IF EXISTS reminder_settings;
ALTER TABLE share_links DROP COLUMN IF EXISTS view_count;
ALTER TABLE share_links DROP COLUMN IF EXISTS last_viewed_at;
ALTER TABLE share_links DROP COLUMN IF EXISTS first_viewed_at;
