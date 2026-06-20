-- Story 206 (B-3): person_credits (PRD v4 §5.3 row "person_credits").
-- Materialised filmography from TMDB /person/{id}/tv_credits +
-- /movie_credits. Natural key (person_id, tmdb_credit_id) — idempotent
-- re-ingest. Powers the "Also in your library" badge on cast strips
-- (composer JOINs against series_cache via tmdb_media_id) and the
-- full filmography list on the person page.
--
-- poster_path is an upstream TMDB path string in v1 — the media
-- downloader picks it up lazily on person-page open; converting to
-- a media_assets.hash reference is deferred to a later media-prewarm
-- story.
--
-- Index (media_type, tmdb_media_id) supports the reverse lookup
-- "who from my library appears in this TMDB title?" — the person
-- page's "More library credits" list.

CREATE TABLE person_credits (
    id              bigserial PRIMARY KEY,
    person_id       bigint  NOT NULL,
    tmdb_credit_id  text    NOT NULL,
    media_type      text    NOT NULL,
    tmdb_media_id   integer NOT NULL,
    title           text    NOT NULL,
    year            integer,
    character_name  text,
    kind            text    NOT NULL,
    job             text,
    poster_path     text,
    vote_average    double precision,
    episode_count   integer,
    created_at      timestamp with time zone NOT NULL,
    updated_at      timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX person_credits_credit ON person_credits (person_id, tmdb_credit_id);
CREATE INDEX person_credits_media ON person_credits (media_type, tmdb_media_id);
CREATE INDEX person_credits_person ON person_credits (person_id);
