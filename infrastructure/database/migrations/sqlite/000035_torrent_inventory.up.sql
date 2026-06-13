-- Story 219 (A-1): sqlite mirror of 000035. Type mapping:
--   * bigserial          → integer PRIMARY KEY AUTOINCREMENT
--   * double precision   → real
--   * boolean            → numeric (0/1)
--   * timestamptz        → datetime
--   * partial index WHERE present → WHERE present = 1

CREATE TABLE qbit_torrents (
    instance_name   text NOT NULL,
    hash            text NOT NULL,
    infohash_v2     text,
    name            text NOT NULL,
    category        text,
    tags            text,
    tracker_host    text,
    save_path       text,
    content_path    text,
    state_raw       text NOT NULL,
    state_group     text NOT NULL,
    size_bytes      integer NOT NULL DEFAULT 0,
    total_size      integer NOT NULL DEFAULT 0,
    downloaded      integer NOT NULL DEFAULT 0,
    uploaded        integer NOT NULL DEFAULT 0,
    ratio           real NOT NULL DEFAULT 0,
    popularity      real NOT NULL DEFAULT 0,
    time_active_s   integer NOT NULL DEFAULT 0,
    seeding_time_s  integer NOT NULL DEFAULT 0,
    added_on        datetime,
    completion_on   datetime,
    last_activity   datetime,
    present         numeric NOT NULL DEFAULT 1,
    deleted_at      datetime,
    first_seen_at   datetime NOT NULL,
    updated_at      datetime NOT NULL,
    PRIMARY KEY (instance_name, hash)
);
CREATE INDEX idx_qbit_torrents_present
    ON qbit_torrents (instance_name)
    WHERE present = 1;

CREATE TABLE torrent_series_map (
    instance_name   text NOT NULL,
    torrent_hash    text NOT NULL,
    series_id       integer NOT NULL,
    season_number   integer,
    source          text NOT NULL,
    created_at      datetime NOT NULL,
    PRIMARY KEY (instance_name, torrent_hash)
);
CREATE INDEX idx_torrent_series_map_series
    ON torrent_series_map (instance_name, series_id);

CREATE TABLE qbit_torrent_events (
    id              integer PRIMARY KEY AUTOINCREMENT,
    instance_name   text NOT NULL,
    torrent_hash    text NOT NULL,
    event           text NOT NULL,
    from_group      text,
    to_group        text,
    occurred_at     datetime NOT NULL
);
CREATE INDEX idx_qbit_torrent_events_hash
    ON qbit_torrent_events (instance_name, torrent_hash, occurred_at);
