-- 044a: nullable parse-metadata columns on grab_records (B2).
-- Array columns stored as JSON-encoded TEXT (NOT native text[]) so the
-- Go model is identical across SQLite and Postgres backends. Trade-off
-- documented in story 044a — F3 Grabs page reads them whole; no
-- WHERE 'HDR10' = ANY(parsed_hdr_flags) query plan exists today.
ALTER TABLE grab_records ADD COLUMN parsed_codec text;
ALTER TABLE grab_records ADD COLUMN parsed_source text;
ALTER TABLE grab_records ADD COLUMN parsed_quality text;
ALTER TABLE grab_records ADD COLUMN parsed_resolution integer;
ALTER TABLE grab_records ADD COLUMN parsed_hdr_flags text;
ALTER TABLE grab_records ADD COLUMN parsed_dub text;
ALTER TABLE grab_records ADD COLUMN parsed_languages text;
ALTER TABLE grab_records ADD COLUMN parsed_subs text;
ALTER TABLE grab_records ADD COLUMN parsed_release_group text;
ALTER TABLE grab_records ADD COLUMN parsed_at timestamp with time zone;

ALTER TABLE sonarr_instance ADD COLUMN parse_on_grab_enabled boolean NOT NULL DEFAULT TRUE;
