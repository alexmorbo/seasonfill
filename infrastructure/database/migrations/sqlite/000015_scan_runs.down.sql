-- D-4 story 465b — reverse of 000015 scan_runs migration on SQLite.
--
-- Atlas's auto-generated `.down.sql` was incorrect (it referenced the
-- `new_grab_records` transient table that no longer exists at runtime
-- and never DROPPed the runtime `grab_records` recreate). This is the
-- hand-rolled mirror of the up migration: recreate `grab_records`
-- without the `grab_records_scan_run_id_fkey` FK, then drop scan_runs.
--
-- The SQL is byte-equivalent to the up `new_grab_records` CREATE minus
-- the scan_runs FK constraint. ON DELETE for sonarr_instance FK is
-- preserved (CASCADE); CHECK status_check preserved.

-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_grab_records" mirroring the original pre-FK shape (no scan_runs FK)
CREATE TABLE `new_grab_records` (
  `id` text NOT NULL,
  `instance_name` text NOT NULL,
  `series_id` bigint NOT NULL,
  `series_title` text NULL,
  `season_number` integer NOT NULL,
  `release_guid` text NULL,
  `release_title` text NULL,
  `download_id` text NULL,
  `indexer_id` integer NULL,
  `indexer_name` text NULL,
  `custom_format_score` integer NOT NULL DEFAULT 0,
  `quality` text NULL,
  `coverage_count` integer NOT NULL DEFAULT 0,
  `status` text NOT NULL DEFAULT 'pending',
  `error_message` text NULL,
  `scan_run_id` text NULL,
  `attempts` integer NOT NULL DEFAULT 0,
  `torrent_hash` text NULL,
  `replay_of_id` text NULL,
  `size_bytes` bigint NULL,
  `parsed_codec` text NULL,
  `parsed_source` text NULL,
  `parsed_quality` text NULL,
  `parsed_resolution` integer NULL,
  `parsed_hdr_flags` text NULL,
  `parsed_dub` text NULL,
  `parsed_languages` text NULL,
  `parsed_subs` text NULL,
  `parsed_release_group` text NULL,
  `parsed_at` datetime NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`id`),
  CONSTRAINT `grab_records_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `grab_records_status_check` CHECK (status IN ('pending', 'grabbed', 'imported', 'failed', 'cancelled'))
);
-- copy rows from current "grab_records" to temporary table
INSERT INTO `new_grab_records` (`id`, `instance_name`, `series_id`, `series_title`, `season_number`, `release_guid`, `release_title`, `download_id`, `indexer_id`, `indexer_name`, `custom_format_score`, `quality`, `coverage_count`, `status`, `error_message`, `scan_run_id`, `attempts`, `torrent_hash`, `replay_of_id`, `size_bytes`, `parsed_codec`, `parsed_source`, `parsed_quality`, `parsed_resolution`, `parsed_hdr_flags`, `parsed_dub`, `parsed_languages`, `parsed_subs`, `parsed_release_group`, `parsed_at`, `created_at`, `updated_at`) SELECT `id`, `instance_name`, `series_id`, `series_title`, `season_number`, `release_guid`, `release_title`, `download_id`, `indexer_id`, `indexer_name`, `custom_format_score`, `quality`, `coverage_count`, `status`, `error_message`, `scan_run_id`, `attempts`, `torrent_hash`, `replay_of_id`, `size_bytes`, `parsed_codec`, `parsed_source`, `parsed_quality`, `parsed_resolution`, `parsed_hdr_flags`, `parsed_dub`, `parsed_languages`, `parsed_subs`, `parsed_release_group`, `parsed_at`, `created_at`, `updated_at` FROM `grab_records`;
-- drop existing "grab_records"
DROP TABLE `grab_records`;
-- rename temporary table to original name
ALTER TABLE `new_grab_records` RENAME TO `grab_records`;
-- recreate all original indexes on grab_records
CREATE INDEX `grab_records_inst_series_idx` ON `grab_records` (`instance_name`, `series_id`, `season_number`);
CREATE INDEX `grab_records_dedupe_lookup_idx` ON `grab_records` (`instance_name`, `series_id`, `season_number`, `release_guid`);
CREATE INDEX `grab_records_release_guid_idx` ON `grab_records` (`release_guid`);
CREATE INDEX `grab_records_download_id_idx` ON `grab_records` (`download_id`);
CREATE INDEX `grab_records_scan_run_idx` ON `grab_records` (`scan_run_id`);
CREATE INDEX `grab_records_status_idx` ON `grab_records` (`status`);
CREATE INDEX `grab_records_inst_created_idx` ON `grab_records` (`instance_name`, `created_at`);
CREATE INDEX `grab_records_replay_of_idx` ON `grab_records` (`replay_of_id`) WHERE replay_of_id IS NOT NULL;
-- drop scan_runs indexes
DROP INDEX `idx_scan_runs_instance_name`;
DROP INDEX `idx_scan_runs_started_at_id`;
DROP INDEX `idx_scan_runs_created_at_id`;
-- drop scan_runs table
DROP TABLE `scan_runs`;
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
