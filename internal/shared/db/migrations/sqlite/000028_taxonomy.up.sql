-- Story 205 (B-2b): SQLite mirror of postgres 000028. Type mapping
-- follows the repo convention (bigserial → integer PRIMARY KEY
-- AUTOINCREMENT, bigint → integer, timestamp with time zone →
-- datetime). Partial unique on tmdb_id is supported (sqlite ≥3.8;
-- glebarez embeds ≥3.45 — verified in 203 acceptance).

CREATE TABLE networks (
    id              integer PRIMARY KEY AUTOINCREMENT,
    tmdb_id         integer,
    name            text    NOT NULL,
    logo_asset      text,
    origin_country  text,
    created_at      datetime NOT NULL,
    updated_at      datetime NOT NULL
);
CREATE UNIQUE INDEX networks_tmdb_id ON networks (tmdb_id) WHERE tmdb_id IS NOT NULL;

CREATE TABLE production_companies (
    id              integer PRIMARY KEY AUTOINCREMENT,
    tmdb_id         integer,
    name            text    NOT NULL,
    logo_asset      text,
    origin_country  text,
    created_at      datetime NOT NULL,
    updated_at      datetime NOT NULL
);
CREATE UNIQUE INDEX production_companies_tmdb_id ON production_companies (tmdb_id) WHERE tmdb_id IS NOT NULL;

CREATE TABLE genres (
    id          integer PRIMARY KEY AUTOINCREMENT,
    tmdb_id     integer,
    created_at  datetime NOT NULL,
    updated_at  datetime NOT NULL
);
CREATE UNIQUE INDEX genres_tmdb_id ON genres (tmdb_id) WHERE tmdb_id IS NOT NULL;

CREATE TABLE genres_i18n (
    genre_id    integer NOT NULL,
    language    text    NOT NULL,
    name        text    NOT NULL,
    updated_at  datetime NOT NULL,
    PRIMARY KEY (genre_id, language)
);
CREATE INDEX genres_i18n_name ON genres_i18n (language, name);

CREATE TABLE keywords (
    id          integer PRIMARY KEY AUTOINCREMENT,
    tmdb_id     integer,
    created_at  datetime NOT NULL,
    updated_at  datetime NOT NULL
);
CREATE UNIQUE INDEX keywords_tmdb_id ON keywords (tmdb_id) WHERE tmdb_id IS NOT NULL;

CREATE TABLE keywords_i18n (
    keyword_id  integer NOT NULL,
    language    text    NOT NULL,
    name        text    NOT NULL,
    updated_at  datetime NOT NULL,
    PRIMARY KEY (keyword_id, language)
);
CREATE INDEX keywords_i18n_name ON keywords_i18n (language, name);

CREATE TABLE series_networks (
    series_id   integer NOT NULL,
    network_id  integer NOT NULL,
    position    integer,
    PRIMARY KEY (series_id, network_id)
);
CREATE INDEX series_networks_network ON series_networks (network_id);

CREATE TABLE series_companies (
    series_id   integer NOT NULL,
    company_id  integer NOT NULL,
    position    integer,
    PRIMARY KEY (series_id, company_id)
);
CREATE INDEX series_companies_company ON series_companies (company_id);

CREATE TABLE series_genres (
    series_id   integer NOT NULL,
    genre_id    integer NOT NULL,
    position    integer,
    PRIMARY KEY (series_id, genre_id)
);
CREATE INDEX series_genres_genre ON series_genres (genre_id);

CREATE TABLE series_keywords (
    series_id   integer NOT NULL,
    keyword_id  integer NOT NULL,
    PRIMARY KEY (series_id, keyword_id)
);
CREATE INDEX series_keywords_keyword ON series_keywords (keyword_id);
