-- Story 206 (B-3): SQLite mirror of postgres 000029. Type mapping
-- follows the repo convention (bigserial → integer PRIMARY KEY
-- AUTOINCREMENT, bigint → integer, timestamp with time zone →
-- datetime, boolean → integer (0|1)). Partial unique on
-- videos.tmdb_video_id is supported (sqlite ≥3.8; glebarez embeds
-- ≥3.45 — verified in 203 acceptance).

CREATE TABLE videos (
    id              integer PRIMARY KEY AUTOINCREMENT,
    series_id       integer NOT NULL,
    tmdb_video_id   text,
    name            text    NOT NULL,
    site            text,
    key             text,
    type            text,
    official        integer NOT NULL DEFAULT 0,
    language        text,
    published_at    datetime,
    created_at      datetime NOT NULL,
    updated_at      datetime NOT NULL
);
CREATE UNIQUE INDEX videos_tmdb_id ON videos (tmdb_video_id) WHERE tmdb_video_id IS NOT NULL;
CREATE INDEX videos_series_type ON videos (series_id, type, official);

CREATE TABLE content_ratings (
    series_id     integer  NOT NULL,
    country_code  text     NOT NULL,
    rating        text     NOT NULL,
    updated_at    datetime NOT NULL,
    PRIMARY KEY (series_id, country_code)
);

CREATE TABLE external_ids (
    entity_type   text     NOT NULL,
    entity_id     integer  NOT NULL,
    provider      text     NOT NULL,
    value         text     NOT NULL,
    updated_at    datetime NOT NULL,
    PRIMARY KEY (entity_type, entity_id, provider)
);
CREATE INDEX external_ids_provider_value ON external_ids (provider, value);

CREATE TABLE series_recommendations (
    series_id              integer  NOT NULL,
    recommended_series_id  integer  NOT NULL,
    position               integer,
    updated_at             datetime NOT NULL,
    PRIMARY KEY (series_id, recommended_series_id)
);
CREATE INDEX series_recommendations_position ON series_recommendations (series_id, position);
