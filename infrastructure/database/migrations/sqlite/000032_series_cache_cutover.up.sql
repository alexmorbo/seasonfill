-- Story 208 (B-1b): SQLite mirror of postgres 000032. See postgres
-- file for full prose. Dialect notes: DISTINCT ON → ROW_NUMBER() CTE;
-- NOW() → CURRENT_TIMESTAMP; PL/pgSQL guard moved to Go-side test.

-- 0. Add `network` text column to canon. See postgres 000032 up §0
-- for the rationale. SQLite ALTER ADD COLUMN does not support
-- IF NOT EXISTS; protect against re-run via the migration
-- bookkeeping (golang-migrate stamps v32 once).
ALTER TABLE series ADD COLUMN network text;

-- 1a. Backfill canon from de-duped non-orphan cache rows. Uses
-- ROW_NUMBER() to mimic postgres DISTINCT ON (latest updated_at wins).
-- poster_asset stays NULL — F-1 resolves later; the existing Sonarr
-- URL is NOT what canon stores.
INSERT INTO series (
    tmdb_id, tvdb_id, imdb_id, title, year, status, network, runtime_minutes,
    last_air_date, poster_asset, backdrop_asset, hydration,
    in_production, created_at, updated_at
)
SELECT tmdb_id, tvdb_id, imdb_id, title, year, status, network, runtime_minutes,
       last_aired_at, NULL, NULL, 'stub', 0,
       CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  FROM (
    SELECT sc.*,
           ROW_NUMBER() OVER (
             PARTITION BY COALESCE(sc.tmdb_id, sc.tvdb_id)
             ORDER BY sc.updated_at DESC
           ) AS rn
      FROM series_cache sc
     WHERE sc.series_id IS NULL
       AND (sc.tmdb_id IS NOT NULL OR sc.tvdb_id IS NOT NULL)
  ) ranked
 WHERE ranked.rn = 1
   AND NOT EXISTS (
     SELECT 1 FROM series s
      WHERE s.tmdb_id IS NOT NULL AND s.tmdb_id = ranked.tmdb_id
   );

-- 1b. Backfill orphans (no tmdb/tvdb id) — one canon per cache row.
INSERT INTO series (
    tmdb_id, tvdb_id, imdb_id, title, year, status, network, runtime_minutes,
    last_air_date, poster_asset, backdrop_asset, hydration,
    in_production, created_at, updated_at
)
SELECT NULL, NULL, imdb_id, title, year, status, network, runtime_minutes,
       last_aired_at, NULL, NULL, 'stub', 0,
       CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  FROM series_cache
 WHERE series_id IS NULL
   AND tmdb_id IS NULL
   AND tvdb_id IS NULL;

-- 2. Stitch series_id — non-orphans by COALESCE(tmdb_id, tvdb_id).
UPDATE series_cache
   SET series_id = (
     SELECT s.id FROM series s
      WHERE (series_cache.tmdb_id IS NOT NULL AND s.tmdb_id = series_cache.tmdb_id)
         OR (series_cache.tmdb_id IS NULL
             AND series_cache.tvdb_id IS NOT NULL
             AND s.tvdb_id = series_cache.tvdb_id)
      LIMIT 1
   )
 WHERE series_id IS NULL
   AND (tmdb_id IS NOT NULL OR tvdb_id IS NOT NULL);

-- Orphans by (title, year) — uniqueness held by 1b's invariant.
UPDATE series_cache
   SET series_id = (
     SELECT s.id FROM series s
      WHERE s.tmdb_id IS NULL AND s.tvdb_id IS NULL
        AND s.title = series_cache.title
        AND ((s.year IS NULL AND series_cache.year IS NULL) OR s.year = series_cache.year)
      LIMIT 1
   )
 WHERE series_id IS NULL
   AND tmdb_id IS NULL
   AND tvdb_id IS NULL;

-- 3. Drop the 14 canon columns. sqlite ≥3.35 supports DROP COLUMN.
ALTER TABLE series_cache DROP COLUMN title;
ALTER TABLE series_cache DROP COLUMN year;
ALTER TABLE series_cache DROP COLUMN tvdb_id;
ALTER TABLE series_cache DROP COLUMN imdb_id;
ALTER TABLE series_cache DROP COLUMN tmdb_id;
ALTER TABLE series_cache DROP COLUMN status;
ALTER TABLE series_cache DROP COLUMN network;
ALTER TABLE series_cache DROP COLUMN genres;
ALTER TABLE series_cache DROP COLUMN runtime_minutes;
ALTER TABLE series_cache DROP COLUMN overview;
ALTER TABLE series_cache DROP COLUMN last_aired_at;
ALTER TABLE series_cache DROP COLUMN poster_path;
ALTER TABLE series_cache DROP COLUMN fanart_path;
ALTER TABLE series_cache DROP COLUMN banner_path;
