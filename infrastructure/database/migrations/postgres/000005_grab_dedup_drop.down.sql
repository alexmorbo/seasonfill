-- Restores the unique 4-tuple index. Will fail if any duplicate rows
-- have accumulated — deduplicate grab_records before running.
DROP INDEX IF EXISTS idx_grab_dedupe_lookup;
CREATE UNIQUE INDEX IF NOT EXISTS idx_grab_dedupe
    ON grab_records (instance_name, series_id, season_number, release_guid);
