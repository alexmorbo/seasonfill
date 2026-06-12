-- Story 203 (B-1a): canonical entity core (PRD v4 §5, §7.1). series
-- is the local canon, instance-independent; series_cache gets
-- app-FK series_id (cutover of canon columns lives in story 208 /
-- B-1b, migration 000035). FK on series_cache.series_id is NOT
-- enforced at the DB level — application-side cascade, as elsewhere
-- in this schema (see SeriesCacheRepository, migration 000011
-- header).
--
-- All `*_texts` tables follow the §5.3 i18n form: composite PK
-- (entity_id, language) on BCP-47 strings, fallback read pattern is
-- the §5.6 helper. Hydration is text(stub|full); upserts default
-- new rows to 'stub' so workers can enrich incrementally.
CREATE TABLE series (
    id                bigserial PRIMARY KEY,
    tmdb_id           integer,
    tvdb_id           integer,
    imdb_id           text,
    hydration         text    NOT NULL DEFAULT 'stub',
    title             text    NOT NULL,
    original_title    text,
    status            text,
    first_air_date    date,
    last_air_date     date,
    next_air_date     timestamp with time zone,
    year              integer,
    runtime_minutes   integer,
    homepage          text,
    original_language text,
    origin_country    text,
    popularity        double precision,
    in_production     boolean NOT NULL DEFAULT FALSE,
    poster_asset      text,
    backdrop_asset    text,
    tmdb_rating       double precision,
    tmdb_votes        integer,
    imdb_rating       double precision,
    imdb_votes        integer,
    omdb_rated        text,
    omdb_awards       text,
    created_at        timestamp with time zone NOT NULL,
    updated_at        timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX series_tmdb_id ON series (tmdb_id) WHERE tmdb_id IS NOT NULL;
CREATE INDEX series_tvdb_id ON series (tvdb_id);
CREATE INDEX series_imdb_id ON series (imdb_id);

CREATE TABLE series_texts (
    series_id   bigint NOT NULL,
    language    text   NOT NULL,
    title       text,
    overview    text,
    tagline     text,
    updated_at  timestamp with time zone NOT NULL,
    PRIMARY KEY (series_id, language)
);

CREATE TABLE seasons (
    id              bigserial PRIMARY KEY,
    series_id       bigint  NOT NULL,
    season_number   integer NOT NULL,
    tmdb_season_id  integer,
    name            text,
    overview        text,
    air_date        date,
    episode_count   integer,
    poster_asset    text,
    created_at      timestamp with time zone NOT NULL,
    updated_at      timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX seasons_natural ON seasons (series_id, season_number);

CREATE TABLE episodes (
    id                   bigserial PRIMARY KEY,
    series_id            bigint  NOT NULL,
    season_id            bigint,
    season_number        integer NOT NULL,
    episode_number       integer NOT NULL,
    tmdb_episode_number  integer,
    tmdb_episode_id      integer,
    sonarr_episode_id    integer,
    absolute_number      integer,
    air_date             timestamp with time zone,
    runtime_minutes      integer,
    finale_type          text,
    still_asset          text,
    tmdb_rating          double precision,
    tmdb_votes           integer,
    created_at           timestamp with time zone NOT NULL,
    updated_at           timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX episodes_natural ON episodes (series_id, season_number, episode_number);
CREATE INDEX episodes_air_date ON episodes (air_date);

CREATE TABLE episode_texts (
    episode_id  bigint NOT NULL,
    language    text   NOT NULL,
    title       text,
    overview    text,
    updated_at  timestamp with time zone NOT NULL,
    PRIMARY KEY (episode_id, language)
);

CREATE TABLE episode_states (
    instance_name    text    NOT NULL,
    episode_id       bigint  NOT NULL,
    monitored        boolean NOT NULL DEFAULT FALSE,
    has_file         boolean NOT NULL DEFAULT FALSE,
    episode_file_id  integer,
    quality          text,
    size_bytes       bigint,
    updated_at       timestamp with time zone NOT NULL,
    PRIMARY KEY (instance_name, episode_id)
);

ALTER TABLE series_cache ADD COLUMN series_id bigint;
CREATE INDEX series_cache_series_id ON series_cache (series_id);
