-- Story 201 (S-1): SQLite mirror of postgres 000024. Type mapping
-- follows the repo convention (bigint -> integer; timestamp with time
-- zone -> datetime). NOT NULL / DEFAULT clauses identical.
CREATE TABLE media_assets (
    hash            text NOT NULL PRIMARY KEY,
    source_url      text NOT NULL,
    kind            text NOT NULL,
    status          text NOT NULL DEFAULT 'pending',
    content_type    text,
    size_bytes      integer,
    fetched_at      datetime,
    last_access_at  datetime,
    created_at      datetime NOT NULL
);
CREATE UNIQUE INDEX idx_media_assets_source_url ON media_assets (source_url);
CREATE INDEX idx_media_assets_gc ON media_assets (status, last_access_at);
