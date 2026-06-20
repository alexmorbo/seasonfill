-- 045a (B3): missing_count column + composite partial index for the
-- dominant list query (instance_name + updated_at desc + sonarr_series_id
-- desc, active rows only). missing_count is the per-series aired-missing
-- episode count, populated at upsert from Sonarr's statistics. Pre-
-- migration rows backfill to 0 and surface as "not missing" in the
-- state=missing filter.
ALTER TABLE series_cache ADD COLUMN missing_count integer NOT NULL DEFAULT 0;

CREATE INDEX idx_series_cache_inst_upd_id
    ON series_cache (instance_name, updated_at DESC, sonarr_series_id DESC)
    WHERE deleted_at IS NULL;
