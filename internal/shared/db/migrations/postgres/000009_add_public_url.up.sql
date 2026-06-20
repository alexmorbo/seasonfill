-- 041: optional public URL for browser-facing links.
-- Sonarr API requests continue to use sonarr_instance.url; the new
-- public_url is consumed only by UI / DTO surfaces (D64). NULL means
-- "fall back to url" — semantics live in runtime.InstanceSnapshot.UIURL.
ALTER TABLE sonarr_instance
    ADD COLUMN public_url text;
