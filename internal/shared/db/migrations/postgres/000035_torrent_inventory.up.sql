-- Story 219 (A-1): qBit inventory foundation. Three tables:
--   * qbit_torrents — per-(instance, hash) snapshot of the last seen
--     qBit state. Upsert semantics; `present` + `deleted_at` flip
--     when a torrent disappears from qBit (torrents_removed delta or
--     full-resync diff). PRD §4.6 — only state_group transitions and
--     mutable counters land here; live telemetry (dlspeed, eta, etc.)
--     never persists.
--   * torrent_series_map — torrent→series bridge populated by the
--     A-3 reconciler (220's webhook capture writes too). PK ensures
--     one (instance_name, hash) maps to exactly one (series_id,
--     season_number) pair.
--   * qbit_torrent_events — state_group transition log + synthetic
--     added/completed/deleted events. 180-day retention via E-2 GC.
--
-- FK convention: none (application-side cascade, see comment in
-- 000011). Per-instance cascade on instance delete is added to
-- SonarrInstanceRepository.Delete in story 220.

CREATE TABLE qbit_torrents (
    instance_name   text NOT NULL,
    hash            text NOT NULL,                   -- v1 lowercase hex; v2 when v1 empty
    infohash_v2     text,
    name            text NOT NULL,
    category        text,
    tags            text,
    tracker_host    text,
    save_path       text,
    content_path    text,
    state_raw       text NOT NULL,
    state_group     text NOT NULL,                   -- see PRD §4.3
    size_bytes      bigint NOT NULL DEFAULT 0,
    total_size      bigint NOT NULL DEFAULT 0,
    downloaded      bigint NOT NULL DEFAULT 0,
    uploaded        bigint NOT NULL DEFAULT 0,
    ratio           double precision NOT NULL DEFAULT 0,
    popularity      double precision NOT NULL DEFAULT 0,
    time_active_s   bigint NOT NULL DEFAULT 0,
    seeding_time_s  bigint NOT NULL DEFAULT 0,
    added_on        timestamp with time zone,
    completion_on   timestamp with time zone,
    last_activity   timestamp with time zone,
    present         boolean NOT NULL DEFAULT true,
    deleted_at      timestamp with time zone,
    first_seen_at   timestamp with time zone NOT NULL,
    updated_at      timestamp with time zone NOT NULL,
    PRIMARY KEY (instance_name, hash)
);
CREATE INDEX idx_qbit_torrents_present
    ON qbit_torrents (instance_name)
    WHERE present;

CREATE TABLE torrent_series_map (
    instance_name   text NOT NULL,
    torrent_hash    text NOT NULL,
    series_id       integer NOT NULL,
    season_number   integer,
    source          text NOT NULL,                   -- webhook|grab_record|queue|history
    created_at      timestamp with time zone NOT NULL,
    PRIMARY KEY (instance_name, torrent_hash)
);
CREATE INDEX idx_torrent_series_map_series
    ON torrent_series_map (instance_name, series_id);

CREATE TABLE qbit_torrent_events (
    id              bigserial PRIMARY KEY,
    instance_name   text NOT NULL,
    torrent_hash    text NOT NULL,
    event           text NOT NULL,                   -- added|state_change|completed|deleted
    from_group      text,
    to_group        text,
    occurred_at     timestamp with time zone NOT NULL
);
CREATE INDEX idx_qbit_torrent_events_hash
    ON qbit_torrent_events (instance_name, torrent_hash, occurred_at);
