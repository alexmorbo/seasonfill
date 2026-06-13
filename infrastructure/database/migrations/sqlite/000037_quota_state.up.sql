-- Story 305: sqlite mirror of 000037. Type mapping:
--   * varchar(64)         → text
--   * timestamptz         → datetime
--   * integer             → integer

CREATE TABLE external_service_quota_state (
    service_name text NOT NULL,
    window_start datetime NOT NULL,
    count        integer NOT NULL DEFAULT 0,
    updated_at   datetime NOT NULL,
    PRIMARY KEY (service_name, window_start)
);

CREATE INDEX idx_external_service_quota_state_window
    ON external_service_quota_state (window_start);
