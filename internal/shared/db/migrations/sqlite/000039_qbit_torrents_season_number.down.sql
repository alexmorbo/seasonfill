-- Plain DROP COLUMN works on sqlite >= 3.35 (2021-03). The pure-Go
-- glebarez/sqlite driver bundles a current sqlite. Same pattern as
-- 000038_person_credits_fields.down.sql.
ALTER TABLE qbit_torrents DROP COLUMN season_number;
