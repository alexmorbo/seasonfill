-- Story 201 (S-1): media asset registry. Content-addressed by sha256
-- of the upstream source URL (see infrastructure/mediastore.Key).
-- One row per stored variant — the bytes live in the object store,
-- this table is the lookup index for the GET /media/{hash} endpoint
-- plus the GC sweep (PRD v4 §6.7).
CREATE TABLE media_assets (
    hash            text NOT NULL PRIMARY KEY,
    source_url      text NOT NULL,
    kind            text NOT NULL,
    status          text NOT NULL DEFAULT 'pending',
    content_type    text,
    size_bytes      bigint,
    fetched_at      timestamp with time zone,
    last_access_at  timestamp with time zone,
    created_at      timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX idx_media_assets_source_url ON media_assets (source_url);
CREATE INDEX idx_media_assets_gc ON media_assets (status, last_access_at);
