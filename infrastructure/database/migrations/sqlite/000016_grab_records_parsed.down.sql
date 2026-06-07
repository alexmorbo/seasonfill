-- SQLite does not support DROP COLUMN in older releases; the glebarez
-- driver ships ≥3.35 which DOES support it. The down migration drops
-- columns in reverse-add order to keep diffs readable on rollback.
ALTER TABLE sonarr_instance DROP COLUMN parse_on_grab_enabled;
ALTER TABLE grab_records DROP COLUMN parsed_at;
ALTER TABLE grab_records DROP COLUMN parsed_release_group;
ALTER TABLE grab_records DROP COLUMN parsed_subs;
ALTER TABLE grab_records DROP COLUMN parsed_languages;
ALTER TABLE grab_records DROP COLUMN parsed_dub;
ALTER TABLE grab_records DROP COLUMN parsed_hdr_flags;
ALTER TABLE grab_records DROP COLUMN parsed_resolution;
ALTER TABLE grab_records DROP COLUMN parsed_quality;
ALTER TABLE grab_records DROP COLUMN parsed_source;
ALTER TABLE grab_records DROP COLUMN parsed_codec;
