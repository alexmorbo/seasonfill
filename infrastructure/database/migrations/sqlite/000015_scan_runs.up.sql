-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_grab_records" table
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
  `status` text NOT NULL DEFAULT 'grabbed',
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
  CONSTRAINT `grab_records_scan_run_id_fkey` FOREIGN KEY (`scan_run_id`) REFERENCES `scan_runs` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT `grab_records_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `grab_records_status_check` CHECK (status IN ('grabbed', 'grab_failed', 'imported', 'import_failed'))
);
-- copy rows from old table "grab_records" to new temporary table "new_grab_records"
INSERT INTO `new_grab_records` (`id`, `instance_name`, `series_id`, `series_title`, `season_number`, `release_guid`, `release_title`, `download_id`, `indexer_id`, `indexer_name`, `custom_format_score`, `quality`, `coverage_count`, `status`, `error_message`, `scan_run_id`, `attempts`, `torrent_hash`, `replay_of_id`, `size_bytes`, `parsed_codec`, `parsed_source`, `parsed_quality`, `parsed_resolution`, `parsed_hdr_flags`, `parsed_dub`, `parsed_languages`, `parsed_subs`, `parsed_release_group`, `parsed_at`, `created_at`, `updated_at`) SELECT `id`, `instance_name`, `series_id`, `series_title`, `season_number`, `release_guid`, `release_title`, `download_id`, `indexer_id`, `indexer_name`, `custom_format_score`, `quality`, `coverage_count`, `status`, `error_message`, `scan_run_id`, `attempts`, `torrent_hash`, `replay_of_id`, `size_bytes`, `parsed_codec`, `parsed_source`, `parsed_quality`, `parsed_resolution`, `parsed_hdr_flags`, `parsed_dub`, `parsed_languages`, `parsed_subs`, `parsed_release_group`, `parsed_at`, `created_at`, `updated_at` FROM `grab_records`;
-- drop "grab_records" table after copying rows
DROP TABLE `grab_records`;
-- rename temporary table "new_grab_records" to "grab_records"
ALTER TABLE `new_grab_records` RENAME TO `grab_records`;
-- create index "grab_records_inst_series_idx" to table: "grab_records"
CREATE INDEX `grab_records_inst_series_idx` ON `grab_records` (`instance_name`, `series_id`, `season_number`);
-- create index "grab_records_dedupe_lookup_idx" to table: "grab_records"
CREATE INDEX `grab_records_dedupe_lookup_idx` ON `grab_records` (`instance_name`, `series_id`, `season_number`, `release_guid`);
-- create index "grab_records_release_guid_idx" to table: "grab_records"
CREATE INDEX `grab_records_release_guid_idx` ON `grab_records` (`release_guid`);
-- create index "grab_records_download_id_idx" to table: "grab_records"
CREATE INDEX `grab_records_download_id_idx` ON `grab_records` (`download_id`);
-- create index "grab_records_scan_run_idx" to table: "grab_records"
CREATE INDEX `grab_records_scan_run_idx` ON `grab_records` (`scan_run_id`);
-- create index "grab_records_status_idx" to table: "grab_records"
CREATE INDEX `grab_records_status_idx` ON `grab_records` (`status`);
-- create index "grab_records_inst_created_idx" to table: "grab_records"
CREATE INDEX `grab_records_inst_created_idx` ON `grab_records` (`instance_name`, `created_at`);
-- create index "grab_records_replay_of_idx" to table: "grab_records"
CREATE INDEX `grab_records_replay_of_idx` ON `grab_records` (`replay_of_id`) WHERE replay_of_id IS NOT NULL;
-- create "scan_runs" table
CREATE TABLE `scan_runs` (
  `id` text NOT NULL,
  `instance_name` text NOT NULL,
  `trigger` text NOT NULL,
  `started_at` datetime NOT NULL,
  `finished_at` datetime NULL,
  `status` text NOT NULL DEFAULT 'running',
  `series_scanned` integer NOT NULL DEFAULT 0,
  `candidates_found` integer NOT NULL DEFAULT 0,
  `grabs_performed` integer NOT NULL DEFAULT 0,
  `grabs_failed` integer NOT NULL DEFAULT 0,
  `errors_count` integer NOT NULL DEFAULT 0,
  `error_message` text NOT NULL DEFAULT '',
  `dry_run` boolean NOT NULL DEFAULT false,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`id`)
);
-- create index "idx_scan_runs_created_at_id" to table: "scan_runs"
CREATE INDEX `idx_scan_runs_created_at_id` ON `scan_runs` (`created_at`, `id`);
-- create index "idx_scan_runs_started_at_id" to table: "scan_runs"
CREATE INDEX `idx_scan_runs_started_at_id` ON `scan_runs` (`started_at`, `id`);
-- create index "idx_scan_runs_instance_name" to table: "scan_runs"
CREATE INDEX `idx_scan_runs_instance_name` ON `scan_runs` (`instance_name`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
