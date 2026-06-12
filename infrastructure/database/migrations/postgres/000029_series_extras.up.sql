-- Story 206 (B-3): series_extras (PRD v4 §5.3, §5.9 row "series_extras").
-- Four canon tables hanging off series — videos, content_ratings,
-- external_ids (polymorphic), series_recommendations. All TMDB-sourced
-- and instance-independent; no FK constraints (application-side per
-- repo convention).
--
-- Partial unique on videos.tmdb_video_id mirrors the series / people /
-- taxonomy partial-unique pattern from 203/204/205 — allows
-- operator-curated rows without a TMDB id (rare) and the planner
-- picks the partial index when ON CONFLICT carries the predicate.
--
-- external_ids is polymorphic: (entity_type, entity_id, provider) PK
-- covers `series`/`person`/`episode` × any provider (`imdb`/`tvdb`/
-- `tmdb`/`wikidata`/`facebook`/`instagram`/`twitter`/...). Hot ids
-- (tmdb/tvdb/imdb) are also denormalised onto canon entities for
-- join-performance; this table is the long-tail catch-all.
--
-- series_recommendations is self-joining on series — recommended_series_id
-- references series.id (typically a stub row). PK
-- (series_id, recommended_series_id) makes Set() replace-all idempotent.

CREATE TABLE videos (
    id              bigserial PRIMARY KEY,
    series_id       bigint  NOT NULL,
    tmdb_video_id   text,
    name            text    NOT NULL,
    site            text,
    key             text,
    type            text,
    official        boolean NOT NULL DEFAULT false,
    language        text,
    published_at    timestamp with time zone,
    created_at      timestamp with time zone NOT NULL,
    updated_at      timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX videos_tmdb_id ON videos (tmdb_video_id) WHERE tmdb_video_id IS NOT NULL;
CREATE INDEX videos_series_type ON videos (series_id, type, official);

CREATE TABLE content_ratings (
    series_id     bigint NOT NULL,
    country_code  text   NOT NULL,
    rating        text   NOT NULL,
    updated_at    timestamp with time zone NOT NULL,
    PRIMARY KEY (series_id, country_code)
);

CREATE TABLE external_ids (
    entity_type   text   NOT NULL,
    entity_id     bigint NOT NULL,
    provider      text   NOT NULL,
    value         text   NOT NULL,
    updated_at    timestamp with time zone NOT NULL,
    PRIMARY KEY (entity_type, entity_id, provider)
);
CREATE INDEX external_ids_provider_value ON external_ids (provider, value);

CREATE TABLE series_recommendations (
    series_id              bigint  NOT NULL,
    recommended_series_id  bigint  NOT NULL,
    position               integer,
    updated_at             timestamp with time zone NOT NULL,
    PRIMARY KEY (series_id, recommended_series_id)
);
CREATE INDEX series_recommendations_position ON series_recommendations (series_id, position);
