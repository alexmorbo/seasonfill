-- 041 down (SQLite). DROP COLUMN supported since SQLite 3.35+.
ALTER TABLE sonarr_instance DROP COLUMN webhook_url_override;
ALTER TABLE sonarr_instance DROP COLUMN webhook_install_enabled;
