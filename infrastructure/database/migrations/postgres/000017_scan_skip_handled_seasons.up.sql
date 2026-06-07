-- 046b: per-instance toggle for the scan pre-filter. NOT NULL DEFAULT TRUE
-- mirrors 044a's parse_on_grab_enabled — metadata-only update on Postgres ≥11,
-- so no row rewrite for the existing instance row.
ALTER TABLE sonarr_instance ADD COLUMN scan_skip_handled_seasons boolean NOT NULL DEFAULT TRUE;
