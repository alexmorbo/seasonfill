-- Story 208 (B-1b): series_cache cutover to thin per-instance projection.
-- Single data-migration of the rebuild (PRD v4 §5.9, §5.11). Three steps:
--   1. Backfill `series` from existing series_cache rows. Dedup by
--      COALESCE(tmdb_id, tvdb_id) so two instances sharing one show
--      yield one canon row. Sonarr orphans (no tmdb/tvdb id) get one
--      canon row per cache row, matched by (instance_name, sonarr_series_id).
--   2. UPDATE series_cache.series_id from the resolved canon ids.
--   3. ALTER TABLE series_cache DROP COLUMN for the 14 canon columns.
--
-- Idempotency: every INSERT filters by `WHERE series_id IS NULL` (or
-- ON CONFLICT DO NOTHING via the partial unique index) so a re-run on
-- a partially-applied DB is a no-op. DROP COLUMN is wrapped in IF
-- EXISTS so it no-ops when the column is gone — safe re-run.
--
-- Down is lossy on per-instance edits (none exist today — Sonarr is
-- the only writer): re-adds the columns and back-fills from canon.

BEGIN;

-- 0. Add `network` text column to canon. 000026 entity_core did not
-- include a network text column on `series` because networks were
-- originally going to live exclusively in the series_networks join
-- (which still exists, populated by TMDB enrichment in E-1+). For
-- B-1b cutover we need a Sonarr-grain network string projection on
-- canon so the SeriesCacheRepository can JOIN s.network without an
-- intermediate join. IF NOT EXISTS keeps re-runs no-op.
ALTER TABLE series ADD COLUMN IF NOT EXISTS network text;

-- 1a. Backfill series from de-duped (tmdb_id, tvdb_id) cache rows. The
-- DISTINCT ON picks the latest by updated_at when two instances
-- disagree (last-write wins per §5.8; values are identical in practice).
-- WHERE series_id IS NULL on series_cache so re-runs touch no rows.
-- poster_asset stays NULL — F-1 media-prewarm resolves hashes later;
-- Sonarr URLs must NOT be written here (breaks F-1 invariant).
INSERT INTO series (
    tmdb_id, tvdb_id, imdb_id, title, year, status, network, runtime_minutes,
    last_air_date, poster_asset, backdrop_asset, hydration,
    in_production, created_at, updated_at
)
SELECT DISTINCT ON (COALESCE(sc.tmdb_id, sc.tvdb_id))
       sc.tmdb_id, sc.tvdb_id, sc.imdb_id, sc.title, sc.year,
       sc.status, sc.network, sc.runtime_minutes,
       sc.last_aired_at,
       NULL, NULL,             -- assets re-resolved by F-1 media-prewarm
       'stub',                 -- workers re-hydrate canon to 'full' later
       FALSE,                  -- canon default; Sonarr write path will refresh
       NOW(), NOW()
  FROM series_cache sc
 WHERE sc.series_id IS NULL
   AND (sc.tmdb_id IS NOT NULL OR sc.tvdb_id IS NOT NULL)
 ORDER BY COALESCE(sc.tmdb_id, sc.tvdb_id), sc.updated_at DESC
ON CONFLICT (tmdb_id) WHERE tmdb_id IS NOT NULL DO NOTHING;

-- 1b. Backfill orphans (no tmdb_id AND no tvdb_id) — one canon row per
-- cache row, no dedup. WHERE series_id IS NULL guards idempotency.
INSERT INTO series (
    tmdb_id, tvdb_id, imdb_id, title, year, status, network, runtime_minutes,
    last_air_date, poster_asset, backdrop_asset, hydration,
    in_production, created_at, updated_at
)
SELECT NULL, NULL, sc.imdb_id, sc.title, sc.year,
       sc.status, sc.network, sc.runtime_minutes,
       sc.last_aired_at,
       NULL, NULL, 'stub', FALSE, NOW(), NOW()
  FROM series_cache sc
 WHERE sc.series_id IS NULL
   AND sc.tmdb_id IS NULL
   AND sc.tvdb_id IS NULL;

-- 2. Stitch series_cache.series_id from the resolved canon rows.
--   - non-orphans: match by COALESCE(tmdb_id, tvdb_id)
--   - orphans: match by (title, year), uniqueness held by the
--     1-canon-per-orphan invariant from 1b
UPDATE series_cache sc
   SET series_id = s.id
  FROM series s
 WHERE sc.series_id IS NULL
   AND ((sc.tmdb_id IS NOT NULL AND s.tmdb_id = sc.tmdb_id)
        OR (sc.tmdb_id IS NULL AND sc.tvdb_id IS NOT NULL AND s.tvdb_id = sc.tvdb_id));

UPDATE series_cache sc
   SET series_id = s.id
  FROM series s
 WHERE sc.series_id IS NULL
   AND sc.tmdb_id IS NULL
   AND sc.tvdb_id IS NULL
   AND s.tmdb_id IS NULL
   AND s.tvdb_id IS NULL
   AND s.title = sc.title
   AND ((s.year IS NULL AND sc.year IS NULL) OR s.year = sc.year);

-- Sanity: every active row MUST have a canon row. The migration is a
-- hard failure if it doesn't — bail out before the DROP COLUMN.
DO $$
DECLARE
    orphan_count int;
BEGIN
    SELECT COUNT(*) INTO orphan_count
      FROM series_cache
     WHERE series_id IS NULL
       AND deleted_at IS NULL;
    IF orphan_count > 0 THEN
        RAISE EXCEPTION
            'series_cache cutover: % active rows have NULL series_id after backfill', orphan_count;
    END IF;
END $$;

-- 3. Drop the canon columns. IF EXISTS makes re-runs safe.
ALTER TABLE series_cache DROP COLUMN IF EXISTS title;
ALTER TABLE series_cache DROP COLUMN IF EXISTS year;
ALTER TABLE series_cache DROP COLUMN IF EXISTS tvdb_id;
ALTER TABLE series_cache DROP COLUMN IF EXISTS imdb_id;
ALTER TABLE series_cache DROP COLUMN IF EXISTS tmdb_id;
ALTER TABLE series_cache DROP COLUMN IF EXISTS status;
ALTER TABLE series_cache DROP COLUMN IF EXISTS network;
ALTER TABLE series_cache DROP COLUMN IF EXISTS genres;
ALTER TABLE series_cache DROP COLUMN IF EXISTS runtime_minutes;
ALTER TABLE series_cache DROP COLUMN IF EXISTS overview;
ALTER TABLE series_cache DROP COLUMN IF EXISTS last_aired_at;
ALTER TABLE series_cache DROP COLUMN IF EXISTS poster_path;
ALTER TABLE series_cache DROP COLUMN IF EXISTS fanart_path;
ALTER TABLE series_cache DROP COLUMN IF EXISTS banner_path;

COMMIT;
