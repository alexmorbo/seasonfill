-- create "series" table
CREATE TABLE `series` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_id` integer NULL,
  `tvdb_id` integer NULL,
  `imdb_id` text NULL,
  `hydration` text NOT NULL DEFAULT 'stub',
  `title` text NOT NULL,
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
  `poster_asset` text NULL,
  `backdrop_asset` text NULL,
  `tmdb_rating` double precision NULL,
  `tmdb_votes` integer NULL,
  `imdb_rating` double precision NULL,
  `imdb_votes` integer NULL,
  `omdb_rated` text NULL,
  `omdb_awards` text NULL,
  `enrichment_tmdb_synced_at` datetime NULL,
  `enrichment_omdb_synced_at` datetime NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
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
-- create "seasons" table
CREATE TABLE `seasons` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `series_id` integer NOT NULL,
  `season_number` integer NOT NULL,
  `tmdb_season_id` integer NULL,
  `name` text NULL,
  `overview` text NULL,
  `air_date` date NULL,
  `episode_count` integer NULL,
  `poster_asset` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `seasons_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "seasons_natural" to table: "seasons"
CREATE UNIQUE INDEX `seasons_natural` ON `seasons` (`series_id`, `season_number`);
-- create "episodes" table
CREATE TABLE `episodes` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `series_id` integer NOT NULL,
  `season_id` integer NULL,
  `season_number` integer NOT NULL,
  `episode_number` integer NOT NULL,
  `tmdb_episode_number` integer NULL,
  `tmdb_episode_id` integer NULL,
  `sonarr_episode_id` integer NULL,
  `absolute_number` integer NULL,
  `air_date` datetime NULL,
  `runtime_minutes` integer NULL,
  `finale_type` text NULL,
  `still_asset` text NULL,
  `tmdb_rating` double precision NULL,
  `tmdb_votes` integer NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `episodes_season_id_fkey` FOREIGN KEY (`season_id`) REFERENCES `seasons` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT `episodes_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "episodes_natural" to table: "episodes"
CREATE UNIQUE INDEX `episodes_natural` ON `episodes` (`series_id`, `season_number`, `episode_number`);
-- create index "episodes_air_date" to table: "episodes"
CREATE INDEX `episodes_air_date` ON `episodes` (`air_date`);
