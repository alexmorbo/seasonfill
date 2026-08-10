-- reverse Ф6-R-1: rebuild arr_instance -> sonarr_instance (drop `type`),
-- re-point all 13 children back to sonarr_instance, drop the radarr
-- settings sibling. Symmetric inverse of the up rebuild (foreign_keys=off
-- envelope, parent first, then instance_secret, then the other children).
PRAGMA foreign_keys = off;

-- rebuild parent: arr_instance -> sonarr_instance, drop `type` column.
CREATE TABLE `new_sonarr_instance` (
  `name` text NOT NULL,
  `url` text NOT NULL,
  `public_url` text NULL,
  `mode` text NOT NULL DEFAULT 'auto',
  `token_secret_id` integer NULL,
  `health` text NOT NULL DEFAULT 'Bootstrapping',
  `last_check_at` datetime NULL,
  `transitions_count` integer NOT NULL DEFAULT 0,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`name`),
  CONSTRAINT `sonarr_instance_token_secret_id_fkey` FOREIGN KEY (`token_secret_id`) REFERENCES `instance_secret` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL
);
INSERT INTO `new_sonarr_instance` (`name`, `url`, `public_url`, `mode`, `token_secret_id`, `health`, `last_check_at`, `transitions_count`, `created_at`, `updated_at`)
  SELECT `name`, `url`, `public_url`, `mode`, `token_secret_id`, `health`, `last_check_at`, `transitions_count`, `created_at`, `updated_at` FROM `arr_instance`;
DROP TABLE `arr_instance`;
ALTER TABLE `new_sonarr_instance` RENAME TO `sonarr_instance`;
CREATE INDEX `sonarr_instance_unhealthy` ON `sonarr_instance` (`last_check_at`) WHERE health <> 'healthy';

-- rebuild instance_secret -> FK sonarr_instance
CREATE TABLE `new_instance_secret` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `instance_name` text NOT NULL,
  `secret_name` text NOT NULL,
  `encrypted_value` bytea NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `instance_secret_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_instance_secret` (`id`, `instance_name`, `secret_name`, `encrypted_value`, `created_at`, `updated_at`)
  SELECT `id`, `instance_name`, `secret_name`, `encrypted_value`, `created_at`, `updated_at` FROM `instance_secret`;
DROP TABLE `instance_secret`;
ALTER TABLE `new_instance_secret` RENAME TO `instance_secret`;
CREATE UNIQUE INDEX `instance_secret_lookup` ON `instance_secret` (`instance_name`, `secret_name`);

-- rebuild user_instance_tags -> FK sonarr_instance (users FK unchanged)
CREATE TABLE `new_user_instance_tags` (
  `user_id` integer NOT NULL,
  `instance_name` text NOT NULL,
  `sonarr_tag_id` integer NOT NULL,
  `sonarr_tag_label` text NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`user_id`, `instance_name`),
  CONSTRAINT `user_instance_tags_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `user_instance_tags_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_user_instance_tags` (`user_id`, `instance_name`, `sonarr_tag_id`, `sonarr_tag_label`, `created_at`, `updated_at`)
  SELECT `user_id`, `instance_name`, `sonarr_tag_id`, `sonarr_tag_label`, `created_at`, `updated_at` FROM `user_instance_tags`;
DROP TABLE `user_instance_tags`;
ALTER TABLE `new_user_instance_tags` RENAME TO `user_instance_tags`;
CREATE UNIQUE INDEX `user_instance_tags_label` ON `user_instance_tags` (`instance_name`, `sonarr_tag_label`);

-- rebuild grab_records -> FK sonarr_instance
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
INSERT INTO `new_grab_records` (`id`, `instance_name`, `series_id`, `series_title`, `season_number`, `release_guid`, `release_title`, `download_id`, `indexer_id`, `indexer_name`, `custom_format_score`, `quality`, `coverage_count`, `status`, `error_message`, `scan_run_id`, `attempts`, `torrent_hash`, `replay_of_id`, `size_bytes`, `parsed_codec`, `parsed_source`, `parsed_quality`, `parsed_resolution`, `parsed_hdr_flags`, `parsed_dub`, `parsed_languages`, `parsed_subs`, `parsed_release_group`, `parsed_at`, `created_at`, `updated_at`)
  SELECT `id`, `instance_name`, `series_id`, `series_title`, `season_number`, `release_guid`, `release_title`, `download_id`, `indexer_id`, `indexer_name`, `custom_format_score`, `quality`, `coverage_count`, `status`, `error_message`, `scan_run_id`, `attempts`, `torrent_hash`, `replay_of_id`, `size_bytes`, `parsed_codec`, `parsed_source`, `parsed_quality`, `parsed_resolution`, `parsed_hdr_flags`, `parsed_dub`, `parsed_languages`, `parsed_subs`, `parsed_release_group`, `parsed_at`, `created_at`, `updated_at` FROM `grab_records`;
DROP TABLE `grab_records`;
ALTER TABLE `new_grab_records` RENAME TO `grab_records`;
CREATE INDEX `grab_records_inst_series_idx` ON `grab_records` (`instance_name`, `series_id`, `season_number`);
CREATE INDEX `grab_records_dedupe_lookup_idx` ON `grab_records` (`instance_name`, `series_id`, `season_number`, `release_guid`);
CREATE INDEX `grab_records_release_guid_idx` ON `grab_records` (`release_guid`);
CREATE INDEX `grab_records_download_id_idx` ON `grab_records` (`download_id`);
CREATE INDEX `grab_records_scan_run_idx` ON `grab_records` (`scan_run_id`);
CREATE INDEX `grab_records_status_idx` ON `grab_records` (`status`);
CREATE INDEX `grab_records_inst_created_idx` ON `grab_records` (`instance_name`, `created_at`);
CREATE INDEX `grab_records_replay_of_idx` ON `grab_records` (`replay_of_id`) WHERE replay_of_id IS NOT NULL;

-- rebuild download_links -> FK sonarr_instance (series FK unchanged)
CREATE TABLE `new_download_links` (
  `qbit_hash` text NOT NULL,
  `instance_name` text NOT NULL,
  `instance_type` text NOT NULL DEFAULT 'sonarr',
  `external_series_id` bigint NULL,
  `external_movie_id` bigint NULL,
  `external_episode_ids` text NULL,
  `global_series_id` integer NULL,
  `discovered_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `source` text NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`qbit_hash`),
  CONSTRAINT `download_links_global_series_id_fkey` FOREIGN KEY (`global_series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT `download_links_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `download_links_type_id_check` CHECK ((instance_type = 'sonarr' AND external_series_id IS NOT NULL AND external_movie_id IS NULL) OR (instance_type = 'radarr' AND external_movie_id IS NOT NULL AND external_series_id IS NULL)),
  CONSTRAINT `download_links_source_check` CHECK (source IN ('webhook', 'arr-poll', 'instance-backfill')),
  CONSTRAINT `download_links_instance_type_check` CHECK (instance_type IN ('sonarr', 'radarr'))
);
INSERT INTO `new_download_links` (`qbit_hash`, `instance_name`, `instance_type`, `external_series_id`, `external_movie_id`, `external_episode_ids`, `global_series_id`, `discovered_at`, `source`, `created_at`, `updated_at`)
  SELECT `qbit_hash`, `instance_name`, `instance_type`, `external_series_id`, `external_movie_id`, `external_episode_ids`, `global_series_id`, `discovered_at`, `source`, `created_at`, `updated_at` FROM `download_links`;
DROP TABLE `download_links`;
ALTER TABLE `new_download_links` RENAME TO `download_links`;
CREATE INDEX `download_links_global_series_idx` ON `download_links` (`global_series_id`);
CREATE INDEX `download_links_instance_source_idx` ON `download_links` (`instance_name`, `source`);
CREATE INDEX `download_links_external_series_idx` ON `download_links` (`instance_name`, `external_series_id`);

-- rebuild watchdog_state -> FK sonarr_instance
CREATE TABLE `new_watchdog_state` (
  `instance_name` text NOT NULL,
  `sonarr_series_id` bigint NOT NULL,
  `season_number` integer NOT NULL,
  `attempt_count` integer NOT NULL DEFAULT 0,
  `last_attempt_at` datetime NOT NULL,
  `cooldown_until` datetime NULL,
  `last_error` text NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`instance_name`, `sonarr_series_id`, `season_number`),
  CONSTRAINT `watchdog_state_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_watchdog_state` (`instance_name`, `sonarr_series_id`, `season_number`, `attempt_count`, `last_attempt_at`, `cooldown_until`, `last_error`, `updated_at`)
  SELECT `instance_name`, `sonarr_series_id`, `season_number`, `attempt_count`, `last_attempt_at`, `cooldown_until`, `last_error`, `updated_at` FROM `watchdog_state`;
DROP TABLE `watchdog_state`;
ALTER TABLE `new_watchdog_state` RENAME TO `watchdog_state`;
CREATE INDEX `watchdog_state_instance_name_idx` ON `watchdog_state` (`instance_name`);
CREATE INDEX `watchdog_state_cooldown_until_idx` ON `watchdog_state` (`cooldown_until`) WHERE cooldown_until IS NOT NULL;

-- rebuild watchdog_blacklist -> FK sonarr_instance
CREATE TABLE `new_watchdog_blacklist` (
  `instance_name` text NOT NULL,
  `sonarr_series_id` bigint NOT NULL,
  `season_number` integer NOT NULL,
  `release_title` text NULL,
  `reason` text NOT NULL,
  `consecutive` integer NOT NULL DEFAULT 0,
  `blacklisted_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `ttl_until` datetime NULL,
  PRIMARY KEY (`instance_name`, `sonarr_series_id`, `season_number`),
  CONSTRAINT `watchdog_blacklist_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_watchdog_blacklist` (`instance_name`, `sonarr_series_id`, `season_number`, `release_title`, `reason`, `consecutive`, `blacklisted_at`, `ttl_until`)
  SELECT `instance_name`, `sonarr_series_id`, `season_number`, `release_title`, `reason`, `consecutive`, `blacklisted_at`, `ttl_until` FROM `watchdog_blacklist`;
DROP TABLE `watchdog_blacklist`;
ALTER TABLE `new_watchdog_blacklist` RENAME TO `watchdog_blacklist`;
CREATE INDEX `watchdog_blacklist_ttl_until_idx` ON `watchdog_blacklist` (`ttl_until`) WHERE ttl_until IS NOT NULL;

-- rebuild sonarr_instance_settings -> FK sonarr_instance
CREATE TABLE `new_sonarr_instance_settings` (
  `instance_name` text NOT NULL,
  `timeout_seconds` integer NOT NULL DEFAULT 10,
  `search_timeout_seconds` integer NOT NULL DEFAULT 60,
  `dry_run` boolean NULL,
  `tags_mode` text NOT NULL DEFAULT 'any',
  `tags_include` text NOT NULL DEFAULT '',
  `tags_exclude` text NOT NULL DEFAULT '',
  `search_require_all_aired` boolean NOT NULL DEFAULT false,
  `search_skip_specials` boolean NOT NULL DEFAULT true,
  `search_skip_anime` boolean NOT NULL DEFAULT false,
  `search_min_custom_format_score` integer NOT NULL DEFAULT 0,
  `ranking_indexer_priority_enabled` boolean NOT NULL DEFAULT false,
  `ranking_origin_bonus` double precision NOT NULL DEFAULT 0,
  `limits_scan_max_series` integer NOT NULL DEFAULT 0,
  `limits_max_grabs_per_scan` integer NOT NULL DEFAULT 0,
  `rate_limit_rpm` integer NOT NULL DEFAULT 30,
  `rate_limit_burst` integer NOT NULL DEFAULT 10,
  `cooldown_mode` text NOT NULL DEFAULT '',
  `cooldown_series_after_grab_sec` integer NOT NULL DEFAULT 0,
  `cooldown_guid_failed_grab_sec` integer NOT NULL DEFAULT 0,
  `cooldown_guid_failed_import_sec` integer NOT NULL DEFAULT 0,
  `retry_max_attempts` integer NOT NULL DEFAULT 0,
  `retry_initial_backoff_sec` integer NOT NULL DEFAULT 0,
  `retry_max_backoff_sec` integer NOT NULL DEFAULT 0,
  `healthcheck_recheck_auth_sec` integer NOT NULL DEFAULT 0,
  `healthcheck_recheck_net_sec` integer NOT NULL DEFAULT 0,
  `public_url` text NULL,
  `webhook_install_enabled` boolean NOT NULL DEFAULT true,
  `webhook_url_override` text NULL,
  `parse_on_grab_enabled` boolean NOT NULL DEFAULT true,
  `scan_skip_handled_seasons` boolean NOT NULL DEFAULT true,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `default_quality_profile_id` integer NULL,
  `default_root_folder_path` text NULL,
  PRIMARY KEY (`instance_name`),
  CONSTRAINT `sonarr_instance_settings_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_sonarr_instance_settings` (`instance_name`, `timeout_seconds`, `search_timeout_seconds`, `dry_run`, `tags_mode`, `tags_include`, `tags_exclude`, `search_require_all_aired`, `search_skip_specials`, `search_skip_anime`, `search_min_custom_format_score`, `ranking_indexer_priority_enabled`, `ranking_origin_bonus`, `limits_scan_max_series`, `limits_max_grabs_per_scan`, `rate_limit_rpm`, `rate_limit_burst`, `cooldown_mode`, `cooldown_series_after_grab_sec`, `cooldown_guid_failed_grab_sec`, `cooldown_guid_failed_import_sec`, `retry_max_attempts`, `retry_initial_backoff_sec`, `retry_max_backoff_sec`, `healthcheck_recheck_auth_sec`, `healthcheck_recheck_net_sec`, `public_url`, `webhook_install_enabled`, `webhook_url_override`, `parse_on_grab_enabled`, `scan_skip_handled_seasons`, `updated_at`, `default_quality_profile_id`, `default_root_folder_path`)
  SELECT `instance_name`, `timeout_seconds`, `search_timeout_seconds`, `dry_run`, `tags_mode`, `tags_include`, `tags_exclude`, `search_require_all_aired`, `search_skip_specials`, `search_skip_anime`, `search_min_custom_format_score`, `ranking_indexer_priority_enabled`, `ranking_origin_bonus`, `limits_scan_max_series`, `limits_max_grabs_per_scan`, `rate_limit_rpm`, `rate_limit_burst`, `cooldown_mode`, `cooldown_series_after_grab_sec`, `cooldown_guid_failed_grab_sec`, `cooldown_guid_failed_import_sec`, `retry_max_attempts`, `retry_initial_backoff_sec`, `retry_max_backoff_sec`, `healthcheck_recheck_auth_sec`, `healthcheck_recheck_net_sec`, `public_url`, `webhook_install_enabled`, `webhook_url_override`, `parse_on_grab_enabled`, `scan_skip_handled_seasons`, `updated_at`, `default_quality_profile_id`, `default_root_folder_path` FROM `sonarr_instance_settings`;
DROP TABLE `sonarr_instance_settings`;
ALTER TABLE `new_sonarr_instance_settings` RENAME TO `sonarr_instance_settings`;

-- rebuild decisions -> FK sonarr_instance
CREATE TABLE `new_decisions` (
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
INSERT INTO `new_decisions` (`id`, `scan_run_id`, `instance_name`, `series_id`, `series_title`, `season_number`, `decision`, `reason`, `missing_count`, `existing_count`, `releases_found`, `candidates_count`, `filtered_out`, `selected_guid`, `selected_data`, `would_grab`, `error_detail`, `superseded_by_id`, `total_episodes`, `aired_episodes`, `existing_episodes`, `grabbed_episodes`, `intent`, `created_at`)
  SELECT `id`, `scan_run_id`, `instance_name`, `series_id`, `series_title`, `season_number`, `decision`, `reason`, `missing_count`, `existing_count`, `releases_found`, `candidates_count`, `filtered_out`, `selected_guid`, `selected_data`, `would_grab`, `error_detail`, `superseded_by_id`, `total_episodes`, `aired_episodes`, `existing_episodes`, `grabbed_episodes`, `intent`, `created_at` FROM `decisions`;
DROP TABLE `decisions`;
ALTER TABLE `new_decisions` RENAME TO `decisions`;
CREATE INDEX `decisions_created_at_id_idx` ON `decisions` (`created_at` DESC, `id` DESC);
CREATE INDEX `decisions_instance_series_idx` ON `decisions` (`instance_name`, `series_id`, `season_number`);
CREATE INDEX `decisions_scan_run_idx` ON `decisions` (`scan_run_id`);

-- rebuild origin_releases -> FK sonarr_instance
CREATE TABLE `new_origin_releases` (
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
INSERT INTO `new_origin_releases` (`instance_name`, `series_id`, `season_number`, `guid`, `indexer_id`, `indexer_name`, `source`, `first_seen_at`, `last_seen_at`, `last_used_at`)
  SELECT `instance_name`, `series_id`, `season_number`, `guid`, `indexer_id`, `indexer_name`, `source`, `first_seen_at`, `last_seen_at`, `last_used_at` FROM `origin_releases`;
DROP TABLE `origin_releases`;
ALTER TABLE `new_origin_releases` RENAME TO `origin_releases`;

-- rebuild qbit_settings -> FK sonarr_instance
CREATE TABLE `new_qbit_settings` (
  `instance_name` text NOT NULL,
  `enabled` boolean NOT NULL DEFAULT false,
  `url` text NOT NULL,
  `username` text NULL,
  `password_encrypted` bytea NULL,
  `category` text NOT NULL DEFAULT 'sonarr',
  `poll_interval_minutes` integer NOT NULL DEFAULT 30,
  `regrab_cooldown_hours` integer NOT NULL DEFAULT 120,
  `max_consecutive_no_better` integer NOT NULL DEFAULT 3,
  `custom_unregistered_msgs` text NOT NULL DEFAULT '[]',
  `qbit_public_url` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`instance_name`),
  CONSTRAINT `qbit_settings_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_qbit_settings` (`instance_name`, `enabled`, `url`, `username`, `password_encrypted`, `category`, `poll_interval_minutes`, `regrab_cooldown_hours`, `max_consecutive_no_better`, `custom_unregistered_msgs`, `qbit_public_url`, `created_at`, `updated_at`)
  SELECT `instance_name`, `enabled`, `url`, `username`, `password_encrypted`, `category`, `poll_interval_minutes`, `regrab_cooldown_hours`, `max_consecutive_no_better`, `custom_unregistered_msgs`, `qbit_public_url`, `created_at`, `updated_at` FROM `qbit_settings`;
DROP TABLE `qbit_settings`;
ALTER TABLE `new_qbit_settings` RENAME TO `qbit_settings`;

-- rebuild qbit_torrents -> FK sonarr_instance
CREATE TABLE `new_qbit_torrents` (
  `instance_name` text NOT NULL,
  `hash` text NOT NULL,
  `infohash_v2` text NULL,
  `name` text NOT NULL,
  `category` text NULL,
  `tags` text NULL,
  `tracker_host` text NULL,
  `save_path` text NULL,
  `content_path` text NULL,
  `state_raw` text NOT NULL,
  `state_group` text NOT NULL,
  `size_bytes` bigint NOT NULL DEFAULT 0,
  `total_size` bigint NOT NULL DEFAULT 0,
  `downloaded` bigint NOT NULL DEFAULT 0,
  `uploaded` bigint NOT NULL DEFAULT 0,
  `ratio` double precision NOT NULL DEFAULT 0,
  `popularity` double precision NOT NULL DEFAULT 0,
  `time_active_s` bigint NOT NULL DEFAULT 0,
  `seeding_time_s` bigint NOT NULL DEFAULT 0,
  `added_on` datetime NULL,
  `completion_on` datetime NULL,
  `last_activity` datetime NULL,
  `season_number` integer NULL,
  `present` boolean NOT NULL DEFAULT true,
  `deleted_at` datetime NULL,
  `first_seen_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`instance_name`, `hash`),
  CONSTRAINT `qbit_torrents_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_qbit_torrents` (`instance_name`, `hash`, `infohash_v2`, `name`, `category`, `tags`, `tracker_host`, `save_path`, `content_path`, `state_raw`, `state_group`, `size_bytes`, `total_size`, `downloaded`, `uploaded`, `ratio`, `popularity`, `time_active_s`, `seeding_time_s`, `added_on`, `completion_on`, `last_activity`, `season_number`, `present`, `deleted_at`, `first_seen_at`, `updated_at`)
  SELECT `instance_name`, `hash`, `infohash_v2`, `name`, `category`, `tags`, `tracker_host`, `save_path`, `content_path`, `state_raw`, `state_group`, `size_bytes`, `total_size`, `downloaded`, `uploaded`, `ratio`, `popularity`, `time_active_s`, `seeding_time_s`, `added_on`, `completion_on`, `last_activity`, `season_number`, `present`, `deleted_at`, `first_seen_at`, `updated_at` FROM `qbit_torrents`;
DROP TABLE `qbit_torrents`;
ALTER TABLE `new_qbit_torrents` RENAME TO `qbit_torrents`;
CREATE INDEX `qbit_torrents_state_group_idx` ON `qbit_torrents` (`instance_name`, `state_group`);

-- rebuild qbit_torrent_events -> FK sonarr_instance
CREATE TABLE `new_qbit_torrent_events` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `instance_name` text NOT NULL,
  `torrent_hash` text NOT NULL,
  `event` text NOT NULL,
  `from_group` text NULL,
  `to_group` text NULL,
  `occurred_at` datetime NOT NULL,
  CONSTRAINT `qbit_torrent_events_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_qbit_torrent_events` (`id`, `instance_name`, `torrent_hash`, `event`, `from_group`, `to_group`, `occurred_at`)
  SELECT `id`, `instance_name`, `torrent_hash`, `event`, `from_group`, `to_group`, `occurred_at` FROM `qbit_torrent_events`;
DROP TABLE `qbit_torrent_events`;
ALTER TABLE `new_qbit_torrent_events` RENAME TO `qbit_torrent_events`;
CREATE INDEX `qbit_torrent_events_occurred_at_idx` ON `qbit_torrent_events` (`occurred_at`);
CREATE INDEX `qbit_torrent_events_instance_hash_idx` ON `qbit_torrent_events` (`instance_name`, `torrent_hash`);

-- rebuild torrent_series_map -> FK sonarr_instance
CREATE TABLE `new_torrent_series_map` (
  `instance_name` text NOT NULL,
  `torrent_hash` text NOT NULL,
  `series_id` bigint NOT NULL,
  `season_number` integer NULL,
  `source` text NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`instance_name`, `torrent_hash`),
  CONSTRAINT `torrent_series_map_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_torrent_series_map` (`instance_name`, `torrent_hash`, `series_id`, `season_number`, `source`, `created_at`)
  SELECT `instance_name`, `torrent_hash`, `series_id`, `season_number`, `source`, `created_at` FROM `torrent_series_map`;
DROP TABLE `torrent_series_map`;
ALTER TABLE `new_torrent_series_map` RENAME TO `torrent_series_map`;
CREATE INDEX `torrent_series_map_series_idx` ON `torrent_series_map` (`instance_name`, `series_id`);

-- drop the radarr settings sibling
DROP TABLE `radarr_instance_settings`;
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
