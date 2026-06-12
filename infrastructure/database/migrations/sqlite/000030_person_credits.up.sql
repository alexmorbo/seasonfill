-- Story 206 (B-3): SQLite mirror of postgres 000030. Type mapping:
-- bigserial → integer PRIMARY KEY AUTOINCREMENT, bigint → integer,
-- double precision → real, timestamp with time zone → datetime.

CREATE TABLE person_credits (
    id              integer PRIMARY KEY AUTOINCREMENT,
    person_id       integer  NOT NULL,
    tmdb_credit_id  text     NOT NULL,
    media_type      text     NOT NULL,
    tmdb_media_id   integer  NOT NULL,
    title           text     NOT NULL,
    year            integer,
    character_name  text,
    kind            text     NOT NULL,
    job             text,
    poster_path     text,
    vote_average    real,
    episode_count   integer,
    created_at      datetime NOT NULL,
    updated_at      datetime NOT NULL
);
CREATE UNIQUE INDEX person_credits_credit ON person_credits (person_id, tmdb_credit_id);
CREATE INDEX person_credits_media ON person_credits (media_type, tmdb_media_id);
CREATE INDEX person_credits_person ON person_credits (person_id);
