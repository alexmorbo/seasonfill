-- 039a down (SQLite). DROP COLUMN works on SQLite 3.35+; the glebarez
-- driver ships ≥3.45. Forward-only is the recommended path in production.
DROP INDEX IF EXISTS idx_grab_records_torrent_hash;
ALTER TABLE grab_records DROP COLUMN torrent_hash;

DROP INDEX IF EXISTS idx_watchdog_blacklist_instance_id;
DROP INDEX IF EXISTS idx_watchdog_blacklist_triple;
DROP TABLE IF EXISTS watchdog_blacklist;

DROP INDEX IF EXISTS idx_instance_qbit_settings_instance_id;
DROP TABLE IF EXISTS instance_qbit_settings;
