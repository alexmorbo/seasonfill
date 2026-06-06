-- 041: webhook auto-install toggle + optional base URL override (D65).
-- Existing rows backfill to webhook_install_enabled=TRUE because the
-- current homelab instance already has the webhook installed; the
-- reconciler will see installed=true and no-op. The override stays
-- NULL until the operator enters a base URL in the General tab.
ALTER TABLE sonarr_instance
    ADD COLUMN webhook_install_enabled boolean NOT NULL DEFAULT TRUE;

ALTER TABLE sonarr_instance
    ADD COLUMN webhook_url_override text;
