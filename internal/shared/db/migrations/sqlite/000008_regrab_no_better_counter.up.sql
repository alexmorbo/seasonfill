-- 039f-1: SQLite mirror of 000008.
CREATE TABLE regrab_no_better_counter (
    id              integer PRIMARY KEY AUTOINCREMENT,
    instance_id     integer NOT NULL,
    series_id       integer NOT NULL,
    season_number   integer NOT NULL,
    consecutive     integer NOT NULL DEFAULT 0,
    last_seen_at    datetime NOT NULL,
    created_at      datetime NOT NULL,
    updated_at      datetime NOT NULL
);

CREATE UNIQUE INDEX idx_regrab_no_better_counter_triple
    ON regrab_no_better_counter (instance_id, series_id, season_number);

CREATE INDEX idx_regrab_no_better_counter_instance_id
    ON regrab_no_better_counter (instance_id);
