-- Story 206 (B-3): per-(entity, source) hydration journal (PRD v4
-- §5.5/§5.6/§7.1) — single source of truth for TTL-staleness,
-- degraded[] composition, and retry backoff. Every sync worker
-- writes one row per fetch attempt.
--
-- PK (entity_type, entity_id, source) — natural key, exactly one row
-- per (canon entity, source).
--
-- sync_log_stale (source, synced_at) — nightly background sweep:
--   WHERE source=? AND outcome='ok' AND synced_at<? ORDER BY synced_at ASC LIMIT ?
--   (the StaleScan repo method).
--
-- sync_log_retry (source, next_attempt_at) WHERE outcome='error' —
-- bounded partial index for the retry-due dispatcher:
--   WHERE outcome='error' AND source=? AND next_attempt_at<=? ORDER BY next_attempt_at ASC LIMIT ?
--   (the RetryDue repo method). PRD §7.1 declared the partial on
--   (next_attempt_at) alone; the two-column form is a strict superset —
--   it supports the same shape PLUS the cheap (source=?) prefix filter.
--
-- entity_type domain (series|season|person) is enforced at the domain
-- layer via the typed enrichment.EntityType enum, not by DB
-- constraint (keeps the table schema-portable across dialects).

CREATE TABLE sync_log (
    entity_type     text    NOT NULL,
    entity_id       bigint  NOT NULL,
    source          text    NOT NULL,
    synced_at       timestamp with time zone,
    outcome         text    NOT NULL DEFAULT 'pending',
    error_detail    text,
    etag            text,
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamp with time zone,
    duration_ms     integer,
    updated_at      timestamp with time zone NOT NULL,
    PRIMARY KEY (entity_type, entity_id, source)
);
CREATE INDEX sync_log_stale ON sync_log (source, synced_at);
CREATE INDEX sync_log_retry ON sync_log (source, next_attempt_at) WHERE outcome = 'error';
