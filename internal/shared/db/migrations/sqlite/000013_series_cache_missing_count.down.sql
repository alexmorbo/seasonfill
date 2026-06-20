-- 045a (B3) down (SQLite).
DROP INDEX IF EXISTS idx_series_cache_inst_upd_id;
ALTER TABLE series_cache DROP COLUMN missing_count;
