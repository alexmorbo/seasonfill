-- reverse: create "origin_releases" table
DROP TABLE `origin_releases`;
-- reverse: create index "decisions_scan_run_idx" to table: "decisions"
DROP INDEX `decisions_scan_run_idx`;
-- reverse: create index "decisions_instance_series_idx" to table: "decisions"
DROP INDEX `decisions_instance_series_idx`;
-- reverse: create index "decisions_created_at_id_idx" to table: "decisions"
DROP INDEX `decisions_created_at_id_idx`;
-- reverse: create "decisions" table
DROP TABLE `decisions`;
-- reverse: create index "cooldowns_expires_at_idx" to table: "cooldowns"
DROP INDEX `cooldowns_expires_at_idx`;
-- reverse: create "cooldowns" table
DROP TABLE `cooldowns`;
-- reverse: drop episode_grabs_episode_id_fkey via SQLite table rebuild.
PRAGMA foreign_keys = off;
CREATE TABLE `old_episode_grabs` (
  `grab_id` text NOT NULL,
  `episode_id` integer NOT NULL,
  `episode_number` integer NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`grab_id`, `episode_id`),
  CONSTRAINT `episode_grabs_episode_id_fkey` FOREIGN KEY (`episode_id`) REFERENCES `episodes` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `episode_grabs_grab_id_fkey` FOREIGN KEY (`grab_id`) REFERENCES `grab_records` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `old_episode_grabs` SELECT * FROM `episode_grabs`;
DROP TABLE `episode_grabs`;
ALTER TABLE `old_episode_grabs` RENAME TO `episode_grabs`;
CREATE INDEX `episode_grabs_episode_idx` ON `episode_grabs` (`episode_id`);
-- reverse: drop grab_records_scan_run_id_fkey via SQLite table rebuild.
CREATE TABLE `old_grab_records` (
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
INSERT INTO `old_grab_records` SELECT * FROM `grab_records`;
DROP TABLE `grab_records`;
ALTER TABLE `old_grab_records` RENAME TO `grab_records`;
CREATE INDEX `grab_records_inst_series_idx` ON `grab_records` (`instance_name`, `series_id`, `season_number`);
CREATE INDEX `grab_records_dedupe_lookup_idx` ON `grab_records` (`instance_name`, `series_id`, `season_number`, `release_guid`);
CREATE INDEX `grab_records_release_guid_idx` ON `grab_records` (`release_guid`);
CREATE INDEX `grab_records_download_id_idx` ON `grab_records` (`download_id`);
CREATE INDEX `grab_records_scan_run_idx` ON `grab_records` (`scan_run_id`);
CREATE INDEX `grab_records_status_idx` ON `grab_records` (`status`);
CREATE INDEX `grab_records_inst_created_idx` ON `grab_records` (`instance_name`, `created_at`);
CREATE INDEX `grab_records_replay_of_idx` ON `grab_records` (`replay_of_id`) WHERE replay_of_id IS NOT NULL;
PRAGMA foreign_keys = on;
