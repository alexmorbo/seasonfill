-- Story 206 (B-3): SQLite mirror of postgres 000031. Partial index
-- WHERE outcome='error' is supported (sqlite ≥3.8 — glebarez ships
-- ≥3.45). Type mapping: bigint → integer, timestamp with time zone →
-- datetime.

CREATE TABLE sync_log (
    entity_type     text     NOT NULL,
    entity_id       integer  NOT NULL,
    source          text     NOT NULL,
    synced_at       datetime,
    outcome         text     NOT NULL DEFAULT 'pending',
    error_detail    text,
    etag            text,
    attempts        integer  NOT NULL DEFAULT 0,
    next_attempt_at datetime,
    duration_ms     integer,
    updated_at      datetime NOT NULL,
    PRIMARY KEY (entity_type, entity_id, source)
);
CREATE INDEX sync_log_stale ON sync_log (source, synced_at);
CREATE INDEX sync_log_retry ON sync_log (source, next_attempt_at) WHERE outcome = 'error';
