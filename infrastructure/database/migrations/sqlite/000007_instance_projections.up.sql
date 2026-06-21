-- create "series_cache" table
CREATE TABLE `series_cache` (
  `instance_name` text NOT NULL,
  `sonarr_series_id` integer NOT NULL,
  `series_id` integer NOT NULL,
  `title_slug` text NOT NULL,
  `monitored` boolean NOT NULL DEFAULT false,
  `missing_count` integer NOT NULL DEFAULT 0,
  `episode_file_count` integer NOT NULL DEFAULT 0,
  `size_on_disk_bytes` integer NOT NULL DEFAULT 0,
  `aired_episode_count` integer NOT NULL DEFAULT 0,
  `updated_at` datetime NOT NULL,
  `deleted_at` datetime NULL,
  PRIMARY KEY (`instance_name`, `sonarr_series_id`),
  CONSTRAINT `series_cache_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "series_cache_instance_active" to table: "series_cache"
CREATE INDEX `series_cache_instance_active` ON `series_cache` (`instance_name`) WHERE deleted_at IS NULL;
-- create index "series_cache_series_id" to table: "series_cache"
CREATE INDEX `series_cache_series_id` ON `series_cache` (`series_id`);
-- create "episode_states" table
CREATE TABLE `episode_states` (
  `instance_name` text NOT NULL,
  `episode_id` integer NOT NULL,
  `monitored` boolean NOT NULL DEFAULT false,
  `has_file` boolean NOT NULL DEFAULT false,
  `episode_file_id` integer NULL,
  `quality` text NULL,
  `size_bytes` integer NULL,
  `video_codec` text NULL,
  `audio_codec` text NULL,
  `audio_channels` text NULL,
  `release_group` text NULL,
  `updated_at` datetime NOT NULL,
  `deleted_at` datetime NULL,
  PRIMARY KEY (`instance_name`, `episode_id`),
  CONSTRAINT `episode_states_episode_id_fkey` FOREIGN KEY (`episode_id`) REFERENCES `episodes` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "episode_states_deleted_at" to table: "episode_states"
CREATE INDEX `episode_states_deleted_at` ON `episode_states` (`instance_name`, `deleted_at`) WHERE deleted_at IS NOT NULL;
-- create "season_stats" table
CREATE TABLE `season_stats` (
  `instance_name` text NOT NULL,
  `sonarr_series_id` integer NOT NULL,
  `season_number` integer NOT NULL,
  `episode_count` integer NOT NULL DEFAULT 0,
  `episode_file_count` integer NOT NULL DEFAULT 0,
  `total_episode_count` integer NOT NULL DEFAULT 0,
  `aired_episode_count` integer NOT NULL DEFAULT 0,
  `monitored` boolean NOT NULL DEFAULT false,
  `size_on_disk_bytes` integer NOT NULL DEFAULT 0,
  `updated_at` datetime NOT NULL,
  `deleted_at` datetime NULL,
  PRIMARY KEY (`instance_name`, `sonarr_series_id`, `season_number`)
);
-- create index "season_stats_series" to table: "season_stats"
CREATE INDEX `season_stats_series` ON `season_stats` (`instance_name`, `sonarr_series_id`) WHERE deleted_at IS NULL;
