-- Story 204 (B-2a): SQLite mirror of postgres 000027. Type mapping
-- follows the repo convention (bigserial → integer PRIMARY KEY
-- AUTOINCREMENT, bigint → integer, smallint → integer, double
-- precision → real, date / timestamp with time zone → datetime).
-- Partial unique index on tmdb_id is supported (sqlite ≥3.8;
-- glebarez embeds ≥3.45 — verified in 203 acceptance).
CREATE TABLE people (
    id                     integer PRIMARY KEY AUTOINCREMENT,
    tmdb_id                integer,
    imdb_id                text,
    hydration              text    NOT NULL DEFAULT 'stub',
    name                   text    NOT NULL,
    original_name          text,
    gender                 integer,
    birthday               datetime,
    deathday               datetime,
    place_of_birth         text,
    known_for_department   text,
    popularity             real,
    profile_asset          text,
    created_at             datetime NOT NULL,
    updated_at             datetime NOT NULL
);
CREATE UNIQUE INDEX people_tmdb_id ON people (tmdb_id) WHERE tmdb_id IS NOT NULL;
CREATE INDEX people_imdb_id ON people (imdb_id);

CREATE TABLE person_biographies (
    person_id   integer NOT NULL,
    language    text    NOT NULL,
    biography   text,
    updated_at  datetime NOT NULL,
    PRIMARY KEY (person_id, language)
);

CREATE TABLE series_people (
    id               integer PRIMARY KEY AUTOINCREMENT,
    series_id        integer NOT NULL,
    person_id        integer NOT NULL,
    kind             text    NOT NULL,
    tmdb_credit_id   text    NOT NULL,
    character_name   text,
    department       text,
    job              text,
    credit_order     integer,
    episode_count    integer,
    created_at       datetime NOT NULL,
    updated_at       datetime NOT NULL
);
CREATE UNIQUE INDEX series_people_credit ON series_people (series_id, tmdb_credit_id);
CREATE INDEX series_people_top ON series_people (series_id, kind, credit_order);
CREATE INDEX series_people_person ON series_people (person_id);

CREATE TABLE episode_people (
    id               integer PRIMARY KEY AUTOINCREMENT,
    episode_id       integer NOT NULL,
    person_id        integer NOT NULL,
    kind             text    NOT NULL,
    tmdb_credit_id   text    NOT NULL,
    character_name   text,
    department       text,
    job              text,
    credit_order     integer,
    created_at       datetime NOT NULL,
    updated_at       datetime NOT NULL
);
CREATE UNIQUE INDEX episode_people_credit ON episode_people (episode_id, tmdb_credit_id);
CREATE INDEX episode_people_person ON episode_people (person_id);
