-- 044a: nullable parse-metadata columns on grab_records (B2).
-- Array columns store JSON-encoded TEXT (gorm:"serializer:json")
-- because SQLite has no native array type and we keep the schema
-- identical to Postgres for cross-DB simplicity. Trade-off: we cannot
-- index or query individual array elements; the F3 Grabs page only
-- reads them as a whole-row blob, so this is acceptable.
ALTER TABLE grab_records ADD COLUMN parsed_codec text;
ALTER TABLE grab_records ADD COLUMN parsed_source text;
ALTER TABLE grab_records ADD COLUMN parsed_quality text;
ALTER TABLE grab_records ADD COLUMN parsed_resolution integer;
ALTER TABLE grab_records ADD COLUMN parsed_hdr_flags text;
ALTER TABLE grab_records ADD COLUMN parsed_dub text;
ALTER TABLE grab_records ADD COLUMN parsed_languages text;
ALTER TABLE grab_records ADD COLUMN parsed_subs text;
ALTER TABLE grab_records ADD COLUMN parsed_release_group text;
ALTER TABLE grab_records ADD COLUMN parsed_at datetime;

-- Per-instance toggle (default ON, matches the migration-defaults
-- precedent set by 000010 webhook_install_enabled).
ALTER TABLE sonarr_instance ADD COLUMN parse_on_grab_enabled numeric NOT NULL DEFAULT 1;
