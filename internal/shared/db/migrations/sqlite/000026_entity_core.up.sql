-- Story 203 (B-1a): SQLite mirror of postgres 000026. Type mapping
-- follows the repo convention (bigserial → integer PRIMARY KEY
-- AUTOINCREMENT, bigint → integer, boolean → numeric, timestamp with
-- time zone → datetime, date → datetime, double precision → real).
-- Partial unique index on tmdb_id is supported (glebarez ships
-- sqlite ≥3.45; partial indexes supported since 3.8).
CREATE TABLE series (
    id                integer PRIMARY KEY AUTOINCREMENT,
    tmdb_id           integer,
    tvdb_id           integer,
    imdb_id           text,
    hydration         text    NOT NULL DEFAULT 'stub',
    title             text    NOT NULL,
    original_title    text,
    status            text,
    first_air_date    datetime,
    last_air_date     datetime,
    next_air_date     datetime,
    year              integer,
    runtime_minutes   integer,
    homepage          text,
    original_language text,
    origin_country    text,
    popularity        real,
    in_production     numeric NOT NULL DEFAULT 0,
    poster_asset      text,
    backdrop_asset    text,
    tmdb_rating       real,
    tmdb_votes        integer,
    imdb_rating       real,
    imdb_votes        integer,
    omdb_rated        text,
    omdb_awards       text,
    created_at        datetime NOT NULL,
    updated_at        datetime NOT NULL
);
CREATE UNIQUE INDEX series_tmdb_id ON series (tmdb_id) WHERE tmdb_id IS NOT NULL;
CREATE INDEX series_tvdb_id ON series (tvdb_id);
CREATE INDEX series_imdb_id ON series (imdb_id);

CREATE TABLE series_texts (
    series_id   integer NOT NULL,
    language    text    NOT NULL,
    title       text,
    overview    text,
    tagline     text,
    updated_at  datetime NOT NULL,
    PRIMARY KEY (series_id, language)
);

CREATE TABLE seasons (
    id              integer PRIMARY KEY AUTOINCREMENT,
    series_id       integer NOT NULL,
    season_number   integer NOT NULL,
    tmdb_season_id  integer,
    name            text,
    overview        text,
    air_date        datetime,
    episode_count   integer,
    poster_asset    text,
    created_at      datetime NOT NULL,
    updated_at      datetime NOT NULL
);
CREATE UNIQUE INDEX seasons_natural ON seasons (series_id, season_number);

CREATE TABLE episodes (
    id                   integer PRIMARY KEY AUTOINCREMENT,
    series_id            integer NOT NULL,
    season_id            integer,
    season_number        integer NOT NULL,
    episode_number       integer NOT NULL,
    tmdb_episode_number  integer,
    tmdb_episode_id      integer,
    sonarr_episode_id    integer,
    absolute_number      integer,
    air_date             datetime,
    runtime_minutes      integer,
    finale_type          text,
    still_asset          text,
    tmdb_rating          real,
    tmdb_votes           integer,
    created_at           datetime NOT NULL,
    updated_at           datetime NOT NULL
);
CREATE UNIQUE INDEX episodes_natural ON episodes (series_id, season_number, episode_number);
CREATE INDEX episodes_air_date ON episodes (air_date);

CREATE TABLE episode_texts (
    episode_id  integer NOT NULL,
    language    text    NOT NULL,
    title       text,
    overview    text,
    updated_at  datetime NOT NULL,
    PRIMARY KEY (episode_id, language)
);

CREATE TABLE episode_states (
    instance_name    text    NOT NULL,
    episode_id       integer NOT NULL,
    monitored        numeric NOT NULL DEFAULT 0,
    has_file         numeric NOT NULL DEFAULT 0,
    episode_file_id  integer,
    quality          text,
    size_bytes       integer,
    updated_at       datetime NOT NULL,
    PRIMARY KEY (instance_name, episode_id)
);

ALTER TABLE series_cache ADD COLUMN series_id integer;
CREATE INDEX series_cache_series_id ON series_cache (series_id);
