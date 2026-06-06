-- 041 down: drop the series cache table.
DROP INDEX IF EXISTS series_cache_instance_active;
DROP TABLE IF EXISTS series_cache;
