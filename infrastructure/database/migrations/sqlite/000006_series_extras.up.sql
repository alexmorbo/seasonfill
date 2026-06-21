-- create "videos" table
CREATE TABLE `videos` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `series_id` integer NOT NULL,
  `tmdb_video_id` text NULL,
  `name` text NOT NULL,
  `site` text NULL,
  `key` text NULL,
  `type` text NULL,
  `official` boolean NOT NULL DEFAULT false,
  `language` text NULL,
  `published_at` datetime NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `videos_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "videos_tmdb_id" to table: "videos"
CREATE UNIQUE INDEX `videos_tmdb_id` ON `videos` (`tmdb_video_id`) WHERE tmdb_video_id IS NOT NULL;
-- create index "videos_series_type" to table: "videos"
CREATE INDEX `videos_series_type` ON `videos` (`series_id`, `type`, `official`);
-- create "content_ratings" table
CREATE TABLE `content_ratings` (
  `series_id` integer NOT NULL,
  `country_code` text NOT NULL,
  `rating` text NOT NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`series_id`, `country_code`),
  CONSTRAINT `content_ratings_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create "external_ids" table
CREATE TABLE `external_ids` (
  `entity_type` text NOT NULL,
  `entity_id` bigint NOT NULL,
  `provider` text NOT NULL,
  `value` text NOT NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`entity_type`, `entity_id`, `provider`)
);
-- create index "external_ids_provider_value" to table: "external_ids"
CREATE INDEX `external_ids_provider_value` ON `external_ids` (`provider`, `value`);
-- create "series_recommendations" table
CREATE TABLE `series_recommendations` (
  `series_id` integer NOT NULL,
  `recommended_series_id` integer NOT NULL,
  `position` integer NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`series_id`, `recommended_series_id`),
  CONSTRAINT `series_recommendations_recommended_series_id_fkey` FOREIGN KEY (`recommended_series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `series_recommendations_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "series_recommendations_position" to table: "series_recommendations"
CREATE INDEX `series_recommendations_position` ON `series_recommendations` (`series_id`, `position`);
