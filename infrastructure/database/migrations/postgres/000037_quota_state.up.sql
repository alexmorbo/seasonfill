-- Story 305: generic DB-persisted rate-limit counter state.
--
-- One row per (service_name, window_start) pair. window_start is the
-- UTC truncated boundary for the active quota window (daily, monthly,
-- or whatever the upstream's reset cadence is — the application
-- layer picks the truncation via internal/runtime/quota helpers).
--
-- count is bumped atomically via `INSERT ... ON CONFLICT DO UPDATE
-- SET count = ... + 1`. Old windows are GC'd by a daily scheduler
-- job (Reset(before=now-7d)) so the table stays tiny — at steady
-- state ~ #services × max-window-retention-days rows.

CREATE TABLE external_service_quota_state (
    service_name varchar(64) NOT NULL,
    window_start timestamp with time zone NOT NULL,
    count        integer NOT NULL DEFAULT 0,
    updated_at   timestamp with time zone NOT NULL,
    PRIMARY KEY (service_name, window_start)
);

-- Single-column index on window_start for the GC sweep
-- (DELETE WHERE window_start < $1). Without it the sweep scans the
-- full table on every run; with it the planner uses an index range
-- scan. Cheap because the PK already indexes (service_name, window_start)
-- but service_name-leading composite is not selective for the sweep.
CREATE INDEX idx_external_service_quota_state_window
    ON external_service_quota_state (window_start);
