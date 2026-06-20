CREATE TABLE season_stats (
    instance_name        TEXT     NOT NULL,
    sonarr_series_id     INTEGER  NOT NULL,
    season_number        INTEGER  NOT NULL,
    episode_count        INTEGER  NOT NULL DEFAULT 0,
    episode_file_count   INTEGER  NOT NULL DEFAULT 0,
    total_episode_count  INTEGER  NOT NULL DEFAULT 0,
    aired_episode_count  INTEGER  NOT NULL DEFAULT 0,
    monitored            BOOLEAN  NOT NULL DEFAULT 0,
    size_on_disk_bytes   BIGINT   NOT NULL DEFAULT 0,
    updated_at           DATETIME NOT NULL,
    deleted_at           DATETIME NULL,
    PRIMARY KEY (instance_name, sonarr_series_id, season_number)
);

CREATE INDEX season_stats_series ON season_stats(instance_name, sonarr_series_id) WHERE deleted_at IS NULL;
