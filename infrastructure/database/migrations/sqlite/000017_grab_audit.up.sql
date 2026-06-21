-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_episode_grabs" table
CREATE TABLE `new_episode_grabs` (
  `grab_id` text NOT NULL,
  `episode_id` integer NOT NULL,
  `episode_number` integer NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`grab_id`, `episode_id`),
  CONSTRAINT `episode_grabs_grab_id_fkey` FOREIGN KEY (`grab_id`) REFERENCES `grab_records` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- copy rows from old table "episode_grabs" to new temporary table "new_episode_grabs"
INSERT INTO `new_episode_grabs` (`grab_id`, `episode_id`, `episode_number`, `created_at`, `updated_at`) SELECT `grab_id`, `episode_id`, `episode_number`, `created_at`, `updated_at` FROM `episode_grabs`;
-- drop "episode_grabs" table after copying rows
DROP TABLE `episode_grabs`;
-- rename temporary table "new_episode_grabs" to "episode_grabs"
ALTER TABLE `new_episode_grabs` RENAME TO `episode_grabs`;
-- create index "episode_grabs_episode_idx" to table: "episode_grabs"
CREATE INDEX `episode_grabs_episode_idx` ON `episode_grabs` (`episode_id`);
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
-- create "cooldowns" table
CREATE TABLE `cooldowns` (
  `scope` text NOT NULL,
  `key` text NOT NULL,
  `expires_at` datetime NOT NULL,
  `reason` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`scope`, `key`)
);
-- create index "cooldowns_expires_at_idx" to table: "cooldowns"
CREATE INDEX `cooldowns_expires_at_idx` ON `cooldowns` (`expires_at`);
-- create "decisions" table
CREATE TABLE `decisions` (
  `id` text NOT NULL,
  `scan_run_id` text NULL,
  `instance_name` text NOT NULL,
  `series_id` bigint NOT NULL,
  `series_title` text NULL,
  `season_number` integer NOT NULL,
  `decision` text NOT NULL,
  `reason` text NULL,
  `missing_count` integer NOT NULL DEFAULT 0,
  `existing_count` integer NOT NULL DEFAULT 0,
  `releases_found` integer NOT NULL DEFAULT 0,
  `candidates_count` integer NOT NULL DEFAULT 0,
  `filtered_out` text NULL,
  `selected_guid` text NULL,
  `selected_data` text NULL,
  `would_grab` boolean NOT NULL DEFAULT false,
  `error_detail` text NULL,
  `superseded_by_id` text NULL,
  `total_episodes` integer NOT NULL DEFAULT 0,
  `aired_episodes` integer NOT NULL DEFAULT 0,
  `existing_episodes` integer NOT NULL DEFAULT 0,
  `grabbed_episodes` integer NOT NULL DEFAULT 0,
  `intent` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`id`),
  CONSTRAINT `decisions_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "decisions_created_at_id_idx" to table: "decisions"
CREATE INDEX `decisions_created_at_id_idx` ON `decisions` (`created_at` DESC, `id` DESC);
-- create index "decisions_instance_series_idx" to table: "decisions"
CREATE INDEX `decisions_instance_series_idx` ON `decisions` (`instance_name`, `series_id`, `season_number`);
-- create index "decisions_scan_run_idx" to table: "decisions"
CREATE INDEX `decisions_scan_run_idx` ON `decisions` (`scan_run_id`);
-- create "origin_releases" table
CREATE TABLE `origin_releases` (
  `instance_name` text NOT NULL,
  `series_id` bigint NOT NULL,
  `season_number` integer NOT NULL,
  `guid` text NOT NULL,
  `indexer_id` integer NOT NULL DEFAULT 0,
  `indexer_name` text NULL,
  `source` text NOT NULL,
  `first_seen_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `last_seen_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `last_used_at` datetime NULL,
  PRIMARY KEY (`instance_name`, `series_id`, `season_number`),
  CONSTRAINT `origin_releases_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
