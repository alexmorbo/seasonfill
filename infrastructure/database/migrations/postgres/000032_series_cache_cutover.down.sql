-- Story 208 (B-1b): down — re-add the 14 canon columns and back-fill
-- values from the canonical `series` row. Lossy on per-instance
-- overrides — none exist as of B-1b (Sonarr is the only writer and
-- writes identical values across instances per §5.8). Operator should
-- NOT down-migrate once E-1 ships and per-instance overrides become
-- possible.

BEGIN;

ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS title           text;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS year            integer;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS tvdb_id         integer;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS imdb_id         text;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS tmdb_id         integer;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS status          text;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS network         text;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS genres          text;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS runtime_minutes integer;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS overview        text;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS last_aired_at   timestamp with time zone;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS poster_path     text;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS fanart_path     text;
ALTER TABLE series_cache ADD COLUMN IF NOT EXISTS banner_path     text;

UPDATE series_cache sc
   SET title           = s.title,
       year            = s.year,
       tvdb_id         = s.tvdb_id,
       imdb_id         = s.imdb_id,
       tmdb_id         = s.tmdb_id,
       status          = s.status,
       network         = s.network,
       runtime_minutes = s.runtime_minutes,
       overview        = NULL,         -- canon overview lives in series_texts post-203
       last_aired_at   = s.last_air_date,
       poster_path     = NULL,         -- F-1 hash-based; cannot reconstruct Sonarr URL
       fanart_path     = NULL,
       banner_path     = NULL
  FROM series s
 WHERE sc.series_id = s.id;

-- title is NOT NULL on pre-cutover schema. Use a placeholder for any
-- row that didn't match (shouldn't exist — every row has series_id
-- post-up — but defensive). Caller will re-hydrate on next scan.
UPDATE series_cache SET title = '' WHERE title IS NULL;

ALTER TABLE series_cache ALTER COLUMN title SET NOT NULL;

-- 0. Drop the network column added in 000032 up.
ALTER TABLE series DROP COLUMN IF EXISTS network;

COMMIT;
