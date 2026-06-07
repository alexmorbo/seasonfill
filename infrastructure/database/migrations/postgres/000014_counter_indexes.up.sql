-- 048: B6 counter aggregation + keyset list speed-ups. IF NOT EXISTS
-- keeps this commutative with B4 (story 046). Non-CONCURRENT CREATE
-- INDEX runs in the migration's implicit transaction; fast on the
-- current homelab dataset. Rewrite with CONCURRENTLY if the table
-- ever grows past the lock-tolerance threshold.
CREATE INDEX IF NOT EXISTS idx_grab_records_instance_created
    ON grab_records (instance_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_scan_runs_instance_created
    ON scan_runs (instance_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_decisions_instance_created
    ON decisions (instance_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_decisions_scan_series_season
    ON decisions (scan_run_id, series_id, season_number);
