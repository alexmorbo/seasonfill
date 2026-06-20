-- 041: per-instance Sonarr series cache (D66). Filled lazily by the
-- scan and queue handlers (041e) and by Sonarr SeriesAdd/SeriesDelete
-- webhook events (041f). Soft-delete keeps grab_records refs valid.
-- No DB-level FK on instance_name — application-side cascade lives in
-- SonarrInstanceRepository.Delete (extended in 041e), consistent with
-- the rest of the schema.
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
    monitored          boolean NOT NULL DEFAULT FALSE,
    overview           text,
    poster_path        text,
    fanart_path        text,
    banner_path        text,
    updated_at         timestamp with time zone NOT NULL,
    deleted_at         timestamp with time zone,
    PRIMARY KEY (instance_name, sonarr_series_id)
);

CREATE INDEX series_cache_instance_active
    ON series_cache (instance_name)
    WHERE deleted_at IS NULL;
