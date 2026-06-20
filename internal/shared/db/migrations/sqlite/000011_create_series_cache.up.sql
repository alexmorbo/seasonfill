-- 041: SQLite mirror of 000011. Booleans become numeric, timestamptz
-- becomes datetime, jsonb arrays become TEXT (the genres column is a
-- JSON-encoded string slice — the repo serialises in/out).
CREATE TABLE series_cache (
    instance_name      text    NOT NULL,
    sonarr_series_id   integer NOT NULL,
    title              text    NOT NULL,
    title_slug         text    NOT NULL,
    year               integer,
    tvdb_id            integer,
    imdb_id            text,
    tmdb_id            integer,
    status             text,
    network            text,
    genres             text,
    runtime_minutes    integer,
    monitored          numeric NOT NULL DEFAULT 0,
    overview           text,
    poster_path        text,
    fanart_path        text,
    banner_path        text,
    updated_at         datetime NOT NULL,
    deleted_at         datetime,
    PRIMARY KEY (instance_name, sonarr_series_id)
);

-- Partial-index support: SQLite ≥3.8; glebarez/go-sqlite ships ≥3.45.
CREATE INDEX series_cache_instance_active
    ON series_cache (instance_name)
    WHERE deleted_at IS NULL;
