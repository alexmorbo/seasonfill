-- Story 208 (B-1b): SQLite mirror of postgres 000032 down. Re-adds
-- the 14 canon columns and back-fills from canon. SQLite cannot
-- retroactively add a NOT NULL constraint via ALTER — title stays
-- NULLable in the restored schema; this is acceptable because the
-- down path is recovery-only, not steady-state.

ALTER TABLE series_cache ADD COLUMN title           text;
ALTER TABLE series_cache ADD COLUMN year            integer;
ALTER TABLE series_cache ADD COLUMN tvdb_id         integer;
ALTER TABLE series_cache ADD COLUMN imdb_id         text;
ALTER TABLE series_cache ADD COLUMN tmdb_id         integer;
ALTER TABLE series_cache ADD COLUMN status          text;
ALTER TABLE series_cache ADD COLUMN network         text;
ALTER TABLE series_cache ADD COLUMN genres          text;
ALTER TABLE series_cache ADD COLUMN runtime_minutes integer;
ALTER TABLE series_cache ADD COLUMN overview        text;
ALTER TABLE series_cache ADD COLUMN last_aired_at   datetime;
ALTER TABLE series_cache ADD COLUMN poster_path     text;
ALTER TABLE series_cache ADD COLUMN fanart_path     text;
ALTER TABLE series_cache ADD COLUMN banner_path     text;

UPDATE series_cache
   SET title           = (SELECT s.title           FROM series s WHERE s.id = series_cache.series_id),
       year            = (SELECT s.year            FROM series s WHERE s.id = series_cache.series_id),
       tvdb_id         = (SELECT s.tvdb_id         FROM series s WHERE s.id = series_cache.series_id),
       imdb_id         = (SELECT s.imdb_id         FROM series s WHERE s.id = series_cache.series_id),
       tmdb_id         = (SELECT s.tmdb_id         FROM series s WHERE s.id = series_cache.series_id),
       status          = (SELECT s.status          FROM series s WHERE s.id = series_cache.series_id),
       network         = (SELECT s.network         FROM series s WHERE s.id = series_cache.series_id),
       runtime_minutes = (SELECT s.runtime_minutes FROM series s WHERE s.id = series_cache.series_id),
       last_aired_at   = (SELECT s.last_air_date   FROM series s WHERE s.id = series_cache.series_id)
 WHERE series_id IS NOT NULL;

UPDATE series_cache SET title = '' WHERE title IS NULL;

-- 0. Drop the network column added in 000032 up. SQLite ≥3.35 supports
-- DROP COLUMN.
ALTER TABLE series DROP COLUMN network;
