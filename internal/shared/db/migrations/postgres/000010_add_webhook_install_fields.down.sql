-- 041 down: drop the webhook-install columns.
ALTER TABLE sonarr_instance DROP COLUMN IF EXISTS webhook_url_override;
ALTER TABLE sonarr_instance DROP COLUMN IF EXISTS webhook_install_enabled;
