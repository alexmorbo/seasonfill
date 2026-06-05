-- 039a down: reverse the foundation. Destroys all Watchdog config and
-- blacklist rows. Forward-only is the recommended path in production.
DROP INDEX IF EXISTS idx_grab_records_torrent_hash;
ALTER TABLE grab_records DROP COLUMN IF EXISTS torrent_hash;

DROP INDEX IF EXISTS idx_watchdog_blacklist_instance_id;
DROP INDEX IF EXISTS idx_watchdog_blacklist_triple;
DROP TABLE IF EXISTS watchdog_blacklist;

DROP INDEX IF EXISTS idx_instance_qbit_settings_instance_id;
DROP TABLE IF EXISTS instance_qbit_settings;
