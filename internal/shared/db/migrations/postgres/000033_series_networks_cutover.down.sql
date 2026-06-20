-- Down: re-add series.network text column and back-fill from the
-- lowest-position series_networks row (mirrors the typical Sonarr
-- single-network shape).
--
-- The down path does NOT delete series_networks rows — the TMDB
-- enrichment worker (C-2) will eventually populate them with richer
-- data; deleting them would lose work. Operator that down-migrates
-- is expected to re-run up afterwards.

BEGIN;

ALTER TABLE series ADD COLUMN IF NOT EXISTS network text;

UPDATE series s
   SET network = sub.name
  FROM (
    SELECT sn.series_id, n.name,
           ROW_NUMBER() OVER (PARTITION BY sn.series_id
                              ORDER BY sn.position NULLS LAST, n.id ASC) AS rn
      FROM series_networks sn
      JOIN networks n ON n.id = sn.network_id
  ) sub
 WHERE sub.series_id = s.id AND sub.rn = 1;

COMMIT;
