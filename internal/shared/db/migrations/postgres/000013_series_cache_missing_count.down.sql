-- 045a (B3) down (Postgres).
DROP INDEX IF EXISTS idx_series_cache_inst_upd_id;
ALTER TABLE series_cache DROP COLUMN IF EXISTS missing_count;
