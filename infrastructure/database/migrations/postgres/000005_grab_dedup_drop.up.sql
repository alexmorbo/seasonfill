-- Multiple grab rows per (instance, series, season, release_guid) are
-- allowed; each attempt is a fresh row. The non-unique index is kept
-- for lookup performance.
DROP INDEX IF EXISTS idx_grab_dedupe;
CREATE INDEX IF NOT EXISTS idx_grab_dedupe_lookup
    ON grab_records (instance_name, series_id, season_number, release_guid);
