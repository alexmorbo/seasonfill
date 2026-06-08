-- 071: SQLite mirror of 000018. SQLite stores TIMESTAMPTZ as datetime;
-- the GORM model uses *time.Time for both engines.
ALTER TABLE series_cache
    ADD COLUMN last_aired_at datetime;
