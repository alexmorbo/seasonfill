-- 039a: Phase 10 Watchdog DB foundation.
-- Adds two new tables and one nullable column. No backfill of existing
-- grab_records — old rows keep torrent_hash NULL per D63.
CREATE TABLE instance_qbit_settings (
    id                          bigserial PRIMARY KEY,
    instance_id                 bigint NOT NULL,
    enabled                     boolean NOT NULL DEFAULT false,
    url                         text NOT NULL,
    username                    text,
    password_encrypted          bytea,
    category                    text NOT NULL DEFAULT 'sonarr',
    poll_interval_minutes       integer NOT NULL DEFAULT 30,
    regrab_cooldown_hours       integer NOT NULL DEFAULT 120,
    max_consecutive_no_better   integer NOT NULL DEFAULT 3,
    custom_unregistered_msgs    jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at                  timestamp with time zone NOT NULL,
    updated_at                  timestamp with time zone NOT NULL
);

CREATE UNIQUE INDEX idx_instance_qbit_settings_instance_id
    ON instance_qbit_settings (instance_id);

CREATE TABLE watchdog_blacklist (
    id              bigserial PRIMARY KEY,
    instance_id     bigint NOT NULL,
    series_id       integer NOT NULL,
    season_number   integer NOT NULL,
    reason          text NOT NULL,
    consecutive     integer NOT NULL,
    created_at      timestamp with time zone NOT NULL,
    expires_at      timestamp with time zone
);

CREATE UNIQUE INDEX idx_watchdog_blacklist_triple
    ON watchdog_blacklist (instance_id, series_id, season_number);

CREATE INDEX idx_watchdog_blacklist_instance_id
    ON watchdog_blacklist (instance_id);

ALTER TABLE grab_records
    ADD COLUMN torrent_hash text;

-- Partial index — saves space on legacy NULL rows.
CREATE INDEX idx_grab_records_torrent_hash
    ON grab_records (torrent_hash)
    WHERE torrent_hash IS NOT NULL;
