-- Story 210 (E-1): SQLite mirror of postgres 000033. Dialect notes:
-- NOW() -> CURRENT_TIMESTAMP; bare DROP COLUMN (modernc's parser does
-- not accept IF EXISTS — see 208 §1.3 note); re-run protection is
-- provided by golang-migrate's schema_migrations bookkeeping.

INSERT INTO networks (name, created_at, updated_at)
SELECT DISTINCT s.network, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
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

ALTER TABLE series DROP COLUMN network;
