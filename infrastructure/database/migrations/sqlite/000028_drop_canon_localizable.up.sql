-- Reviewed destructive migration (S-E3b): localizable canon columns move
-- to the *_texts / *_media_texts side tables (S-C / S-C2 / 584a). All
-- readers repointed in S-E3a. modernc SQLite has no per-column DROP that
-- atlas will emit here, so it rebuilds series/seasons (copy rows → drop →
-- rename) inside the PRAGMA foreign_keys wrap. Down re-adds them NULLABLE.
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_series" table
-- atlas:nolint destructive
CREATE TABLE `new_series` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_id` integer NULL,
  `tvdb_id` integer NULL,
  `imdb_id` text NULL,
  `hydration` text NOT NULL DEFAULT 'stub',
  `original_title` text NULL,
  `status` text NULL,
  `first_air_date` date NULL,
  `last_air_date` date NULL,
  `next_air_date` datetime NULL,
  `year` integer NULL,
  `runtime_minutes` integer NULL,
  `homepage` text NULL,
  `original_language` text NULL,
  `origin_country` text NULL,
  `origin_countries` text NOT NULL DEFAULT '[]',
  `tmdb_type` integer NULL,
  `popularity` double precision NULL,
  `in_production` boolean NOT NULL DEFAULT false,
  `tmdb_rating` double precision NULL,
  `tmdb_votes` integer NULL,
  `imdb_rating` double precision NULL,
  `imdb_votes` integer NULL,
  `omdb_rated` text NULL,
  `omdb_awards` text NULL,
  `enrichment_tmdb_synced_at` datetime NULL,
  `enrichment_omdb_synced_at` datetime NULL,
  `enrichment_text_synced_at` datetime NULL,
  `enrichment_cast_synced_at` datetime NULL,
  `enrichment_recs_synced_at` datetime NULL,
  `enrichment_media_synced_at` datetime NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- copy rows from old table "series" to new temporary table "new_series"
INSERT INTO `new_series` (`id`, `tmdb_id`, `tvdb_id`, `imdb_id`, `hydration`, `original_title`, `status`, `first_air_date`, `last_air_date`, `next_air_date`, `year`, `runtime_minutes`, `homepage`, `original_language`, `origin_country`, `origin_countries`, `tmdb_type`, `popularity`, `in_production`, `tmdb_rating`, `tmdb_votes`, `imdb_rating`, `imdb_votes`, `omdb_rated`, `omdb_awards`, `enrichment_tmdb_synced_at`, `enrichment_omdb_synced_at`, `enrichment_text_synced_at`, `enrichment_cast_synced_at`, `enrichment_recs_synced_at`, `enrichment_media_synced_at`, `created_at`, `updated_at`) SELECT `id`, `tmdb_id`, `tvdb_id`, `imdb_id`, `hydration`, `original_title`, `status`, `first_air_date`, `last_air_date`, `next_air_date`, `year`, `runtime_minutes`, `homepage`, `original_language`, `origin_country`, `origin_countries`, `tmdb_type`, `popularity`, `in_production`, `tmdb_rating`, `tmdb_votes`, `imdb_rating`, `imdb_votes`, `omdb_rated`, `omdb_awards`, `enrichment_tmdb_synced_at`, `enrichment_omdb_synced_at`, `enrichment_text_synced_at`, `enrichment_cast_synced_at`, `enrichment_recs_synced_at`, `enrichment_media_synced_at`, `created_at`, `updated_at` FROM `series`;
-- drop "series" table after copying rows
DROP TABLE `series`;
-- rename temporary table "new_series" to "series"
ALTER TABLE `new_series` RENAME TO `series`;
-- create index "series_tmdb_id_idx" to table: "series"
CREATE UNIQUE INDEX `series_tmdb_id_idx` ON `series` (`tmdb_id`) WHERE tmdb_id IS NOT NULL;
-- create index "series_imdb_id_idx" to table: "series"
CREATE INDEX `series_imdb_id_idx` ON `series` (`imdb_id`);
-- create index "series_tvdb_id_idx" to table: "series"
CREATE INDEX `series_tvdb_id_idx` ON `series` (`tvdb_id`);
-- create index "series_popularity_idx" to table: "series"
CREATE INDEX `series_popularity_idx` ON `series` (`popularity` DESC);
-- create index "series_tmdb_rating_idx" to table: "series"
CREATE INDEX `series_tmdb_rating_idx` ON `series` (`tmdb_rating` DESC);
-- create index "series_tmdb_type_idx" to table: "series"
CREATE INDEX `series_tmdb_type_idx` ON `series` (`tmdb_type`) WHERE tmdb_type IS NOT NULL;
-- create index "series_enrichment_text_synced_at_idx" to table: "series"
CREATE INDEX `series_enrichment_text_synced_at_idx` ON `series` (`enrichment_text_synced_at`);
-- create index "series_enrichment_cast_synced_at_idx" to table: "series"
CREATE INDEX `series_enrichment_cast_synced_at_idx` ON `series` (`enrichment_cast_synced_at`);
-- create index "series_enrichment_recs_synced_at_idx" to table: "series"
CREATE INDEX `series_enrichment_recs_synced_at_idx` ON `series` (`enrichment_recs_synced_at`);
-- create index "series_enrichment_media_synced_at_idx" to table: "series"
CREATE INDEX `series_enrichment_media_synced_at_idx` ON `series` (`enrichment_media_synced_at`);
-- create "new_seasons" table
-- atlas:nolint destructive
CREATE TABLE `new_seasons` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `series_id` integer NOT NULL,
  `season_number` integer NOT NULL,
  `tmdb_season_id` integer NULL,
  `air_date` date NULL,
  `episode_count` integer NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `episodes_synced_at` datetime NULL,
  CONSTRAINT `seasons_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- copy rows from old table "seasons" to new temporary table "new_seasons"
INSERT INTO `new_seasons` (`id`, `series_id`, `season_number`, `tmdb_season_id`, `air_date`, `episode_count`, `created_at`, `updated_at`, `episodes_synced_at`) SELECT `id`, `series_id`, `season_number`, `tmdb_season_id`, `air_date`, `episode_count`, `created_at`, `updated_at`, `episodes_synced_at` FROM `seasons`;
-- drop "seasons" table after copying rows
DROP TABLE `seasons`;
-- rename temporary table "new_seasons" to "seasons"
ALTER TABLE `new_seasons` RENAME TO `seasons`;
-- create index "seasons_natural" to table: "seasons"
CREATE UNIQUE INDEX `seasons_natural` ON `seasons` (`series_id`, `season_number`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
