-- 046b: per-instance toggle for the scan pre-filter. Default TRUE so
-- existing rows opt-in to the optimisation without an operator touching
-- the form (same precedent as 044a parse_on_grab_enabled and 041 webhook_install_enabled).
-- SQLite booleans are stored as numeric — `1` matches the gorm tag's
-- `default:true` rendering.
ALTER TABLE sonarr_instance ADD COLUMN scan_skip_handled_seasons numeric NOT NULL DEFAULT 1;
