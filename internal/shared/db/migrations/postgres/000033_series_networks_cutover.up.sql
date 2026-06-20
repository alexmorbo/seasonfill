-- Story 210 (E-1): drop the temporary series.network text column
-- (added in 208 as a B-1b unblock) and route every existing value
-- through the canonical series_networks join. The 208 ADR explicitly
-- defers this drop to E-1 so the catalog readers and watchdog kept
-- working without touching series_networks during the 208 deploy.
--
-- Three steps:
--   1. Backfill missing networks rows from distinct series.network
--      strings. networks does NOT have UQ on name (only partial UQ on
--      tmdb_id); we filter via LEFT JOIN to avoid duplicate inserts.
--   2. Backfill series_networks rows from each (series_id, network)
--      pairing. ON CONFLICT (series_id, network_id) DO NOTHING covers
--      re-runs of the migration.
--   3. Drop the series.network column.
--
-- Idempotency: re-running the migration on a partially-applied DB is
-- a no-op (LEFT JOIN gates step 1, ON CONFLICT gates step 2, DROP
-- COLUMN IF EXISTS gates step 3).

BEGIN;

INSERT INTO networks (name, created_at, updated_at)
SELECT DISTINCT s.network, NOW(), NOW()
  FROM series s
  LEFT JOIN networks n ON n.name = s.network
 WHERE s.network IS NOT NULL
   AND s.network <> ''
   AND n.id IS NULL;

INSERT INTO series_networks (series_id, network_id, position)
SELECT s.id, n.id, 0
  FROM series s
  JOIN networks n ON n.name = s.network
 WHERE s.network IS NOT NULL
   AND s.network <> ''
ON CONFLICT (series_id, network_id) DO NOTHING;

ALTER TABLE series DROP COLUMN IF EXISTS network;

COMMIT;
