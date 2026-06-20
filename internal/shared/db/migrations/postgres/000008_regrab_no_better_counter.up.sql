-- 039f-1: live counter of consecutive "nothing better" evaluate outcomes
-- per (instance, series, season). Separate from watchdog_blacklist
-- because the blacklist holds *escalated* triples only (consecutive >=
-- threshold), and mixing live counters with escalated rows muddies
-- every blacklist query. Once the counter hits the per-instance
-- max_consecutive_no_better, the regrab use case copies the row into
-- watchdog_blacklist and Resets the counter to zero.
CREATE TABLE regrab_no_better_counter (
    id              bigserial PRIMARY KEY,
    instance_id     bigint NOT NULL,
    series_id       integer NOT NULL,
    season_number   integer NOT NULL,
    consecutive     integer NOT NULL DEFAULT 0,
    last_seen_at    timestamp with time zone NOT NULL,
    created_at      timestamp with time zone NOT NULL,
    updated_at      timestamp with time zone NOT NULL
);

CREATE UNIQUE INDEX idx_regrab_no_better_counter_triple
    ON regrab_no_better_counter (instance_id, series_id, season_number);

CREATE INDEX idx_regrab_no_better_counter_instance_id
    ON regrab_no_better_counter (instance_id);
