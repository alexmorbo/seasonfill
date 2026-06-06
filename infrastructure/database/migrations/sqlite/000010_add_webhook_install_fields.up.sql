-- 041: SQLite mirror of 000010. Boolean is numeric (0/1) in SQLite,
-- matching the `enabled` column on instance_qbit_settings (000006).
ALTER TABLE sonarr_instance ADD COLUMN webhook_install_enabled numeric NOT NULL DEFAULT 1;
ALTER TABLE sonarr_instance ADD COLUMN webhook_url_override text;
