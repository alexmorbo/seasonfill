-- Story 204 (B-2a): people domain (PRD v4 §5.3, §7.1). people is the
-- canonical, instance-independent person row; series_people /
-- episode_people materialise TMDB aggregate_credits / per-episode
-- credits. Biographies live in person_biographies on the §5.3
-- (entity_id, language) form — read via the shared §5.6 fallback
-- helper from story 203 (i18n_texts.go::pickLanguageFallback).
--
-- name / original_name STAY on people: TMDB does not localise person
-- names reliably (also_known_as is curated only for headline talent).
-- This is the only canon i18n exception in the schema — see §5.3
-- row 11 + §5.4 table row "people.*".
--
-- Hydration is text(stub|full); upserts default new rows to 'stub'
-- so the series_enrichment_worker (C-2) can stub-create referenced
-- people from aggregate_credits, and person_enrichment_worker (C-3)
-- lifts them to 'full' on background fetch.
CREATE TABLE people (
    id                     bigserial PRIMARY KEY,
    tmdb_id                integer,
    imdb_id                text,
    hydration              text    NOT NULL DEFAULT 'stub',
    name                   text    NOT NULL,
    original_name          text,
    gender                 smallint,
    birthday               date,
    deathday               date,
    place_of_birth         text,
    known_for_department   text,
    popularity             double precision,
    profile_asset          text,
    created_at             timestamp with time zone NOT NULL,
    updated_at             timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX people_tmdb_id ON people (tmdb_id) WHERE tmdb_id IS NOT NULL;
CREATE INDEX people_imdb_id ON people (imdb_id);

CREATE TABLE person_biographies (
    person_id   bigint NOT NULL,
    language    text   NOT NULL,
    biography   text,
    updated_at  timestamp with time zone NOT NULL,
    PRIMARY KEY (person_id, language)
);

CREATE TABLE series_people (
    id               bigserial PRIMARY KEY,
    series_id        bigint  NOT NULL,
    person_id        bigint  NOT NULL,
    kind             text    NOT NULL,
    tmdb_credit_id   text    NOT NULL,
    character_name   text,
    department       text,
    job              text,
    credit_order     integer,
    episode_count    integer,
    created_at       timestamp with time zone NOT NULL,
    updated_at       timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX series_people_credit ON series_people (series_id, tmdb_credit_id);
CREATE INDEX series_people_top ON series_people (series_id, kind, credit_order);
CREATE INDEX series_people_person ON series_people (person_id);

CREATE TABLE episode_people (
    id               bigserial PRIMARY KEY,
    episode_id       bigint  NOT NULL,
    person_id        bigint  NOT NULL,
    kind             text    NOT NULL,
    tmdb_credit_id   text    NOT NULL,
    character_name   text,
    department       text,
    job              text,
    credit_order     integer,
    created_at       timestamp with time zone NOT NULL,
    updated_at       timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX episode_people_credit ON episode_people (episode_id, tmdb_credit_id);
CREATE INDEX episode_people_person ON episode_people (person_id);
