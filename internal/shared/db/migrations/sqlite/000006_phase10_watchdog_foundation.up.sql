-- 039a: Phase 10 Watchdog DB foundation (SQLite mirror).
-- jsonb is TEXT in SQLite; bytea is BLOB; timestamptz is datetime.
CREATE TABLE instance_qbit_settings (
    id                          integer PRIMARY KEY AUTOINCREMENT,
    instance_id                 integer NOT NULL,
    enabled                     numeric NOT NULL DEFAULT 0,
    url                         text NOT NULL,
    username                    text,
    password_encrypted          blob,
    category                    text NOT NULL DEFAULT 'sonarr',
    poll_interval_minutes       integer NOT NULL DEFAULT 30,
    regrab_cooldown_hours       integer NOT NULL DEFAULT 120,
    max_consecutive_no_better   integer NOT NULL DEFAULT 3,
    custom_unregistered_msgs    text NOT NULL DEFAULT '[]',
    created_at                  datetime NOT NULL,
    updated_at                  datetime NOT NULL
);

CREATE UNIQUE INDEX idx_instance_qbit_settings_instance_id
    ON instance_qbit_settings (instance_id);

CREATE TABLE watchdog_blacklist (
    id              integer PRIMARY KEY AUTOINCREMENT,
    instance_id     integer NOT NULL,
    series_id       integer NOT NULL,
    season_number   integer NOT NULL,
    reason          text NOT NULL,
    consecutive     integer NOT NULL,
    created_at      datetime NOT NULL,
    expires_at      datetime
);

CREATE UNIQUE INDEX idx_watchdog_blacklist_triple
    ON watchdog_blacklist (instance_id, series_id, season_number);

CREATE INDEX idx_watchdog_blacklist_instance_id
    ON watchdog_blacklist (instance_id);

ALTER TABLE grab_records ADD COLUMN torrent_hash text;

-- SQLite partial-index support since 3.8 — current glebarez/go-sqlite
-- ships well above that.
CREATE INDEX idx_grab_records_torrent_hash
    ON grab_records (torrent_hash)
    WHERE torrent_hash IS NOT NULL;
