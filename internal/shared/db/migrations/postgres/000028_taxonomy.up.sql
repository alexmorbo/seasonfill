-- Story 205 (B-2b): taxonomy domain (PRD v4 §5.3, §5.4). Four canon
-- dictionaries (networks, production_companies, genres, keywords) +
-- their four series joins. networks / production_companies keep
-- `name` on the entity (not meaningfully translated); genres /
-- keywords carry names in a sibling i18n table on the §5.3
-- (entity_id, language) form — read via the shared §5.6 fallback
-- helper from story 203 (i18n_texts.go::pickLanguageFallback).
--
-- Partial unique on tmdb_id mirrors series (203) / people (204) so
-- the Sonarr-fallback path (PRD §5.4 row "series_networks") can
-- create a network from a string without a TMDB id.
--
-- genres_i18n_name (language, name) is the lookup index for the
-- PRD §5.4 Sonarr-genre fallback: WHERE language='en-US' AND
-- name='Drama' resolves to a canonical genres.id.

CREATE TABLE networks (
    id              bigserial PRIMARY KEY,
    tmdb_id         integer,
    name            text    NOT NULL,
    logo_asset      text,
    origin_country  text,
    created_at      timestamp with time zone NOT NULL,
    updated_at      timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX networks_tmdb_id ON networks (tmdb_id) WHERE tmdb_id IS NOT NULL;

CREATE TABLE production_companies (
    id              bigserial PRIMARY KEY,
    tmdb_id         integer,
    name            text    NOT NULL,
    logo_asset      text,
    origin_country  text,
    created_at      timestamp with time zone NOT NULL,
    updated_at      timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX production_companies_tmdb_id ON production_companies (tmdb_id) WHERE tmdb_id IS NOT NULL;

CREATE TABLE genres (
    id          bigserial PRIMARY KEY,
    tmdb_id     integer,
    created_at  timestamp with time zone NOT NULL,
    updated_at  timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX genres_tmdb_id ON genres (tmdb_id) WHERE tmdb_id IS NOT NULL;

CREATE TABLE genres_i18n (
    genre_id    bigint NOT NULL,
    language    text   NOT NULL,
    name        text   NOT NULL,
    updated_at  timestamp with time zone NOT NULL,
    PRIMARY KEY (genre_id, language)
);
CREATE INDEX genres_i18n_name ON genres_i18n (language, name);

CREATE TABLE keywords (
    id          bigserial PRIMARY KEY,
    tmdb_id     integer,
    created_at  timestamp with time zone NOT NULL,
    updated_at  timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX keywords_tmdb_id ON keywords (tmdb_id) WHERE tmdb_id IS NOT NULL;

CREATE TABLE keywords_i18n (
    keyword_id  bigint NOT NULL,
    language    text   NOT NULL,
    name        text   NOT NULL,
    updated_at  timestamp with time zone NOT NULL,
    PRIMARY KEY (keyword_id, language)
);
CREATE INDEX keywords_i18n_name ON keywords_i18n (language, name);

CREATE TABLE series_networks (
    series_id   bigint  NOT NULL,
    network_id  bigint  NOT NULL,
    position    integer,
    PRIMARY KEY (series_id, network_id)
);
CREATE INDEX series_networks_network ON series_networks (network_id);

CREATE TABLE series_companies (
    series_id   bigint  NOT NULL,
    company_id  bigint  NOT NULL,
    position    integer,
    PRIMARY KEY (series_id, company_id)
);
CREATE INDEX series_companies_company ON series_companies (company_id);

CREATE TABLE series_genres (
    series_id   bigint  NOT NULL,
    genre_id    bigint  NOT NULL,
    position    integer,
    PRIMARY KEY (series_id, genre_id)
);
CREATE INDEX series_genres_genre ON series_genres (genre_id);

CREATE TABLE series_keywords (
    series_id   bigint  NOT NULL,
    keyword_id  bigint  NOT NULL,
    PRIMARY KEY (series_id, keyword_id)
);
CREATE INDEX series_keywords_keyword ON series_keywords (keyword_id);
