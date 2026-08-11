-- Ф6-R-3: movie canon vertical (sqlite dialect). Additive only.
-- create "movies" table
CREATE TABLE `movies` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_id` integer NULL,
  `imdb_id` text NULL,
  `hydration` text NOT NULL DEFAULT 'stub',
  `title` text NOT NULL,
  `original_title` text NULL,
  `status` text NULL,
  `release_date` date NULL,
  `digital_release_date` date NULL,
  `physical_release_date` date NULL,
  `year` integer NULL,
  `runtime_minutes` integer NULL,
  `homepage` text NULL,
  `original_language` text NULL,
  `origin_countries` text NOT NULL DEFAULT '[]',
  `collection_id` integer NULL,
  `popularity` double precision NULL,
  `budget` integer NULL,
  `revenue` integer NULL,
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
CREATE UNIQUE INDEX `movies_tmdb_id_idx` ON `movies` (`tmdb_id`) WHERE tmdb_id IS NOT NULL;
CREATE INDEX `movies_imdb_id_idx` ON `movies` (`imdb_id`);
CREATE INDEX `movies_popularity_idx` ON `movies` (`popularity` DESC);
CREATE INDEX `movies_tmdb_rating_idx` ON `movies` (`tmdb_rating` DESC);
CREATE INDEX `movies_collection_id_idx` ON `movies` (`collection_id`) WHERE collection_id IS NOT NULL;
-- create "movie_i18n" table
CREATE TABLE `movie_i18n` (
  `movie_id` integer NOT NULL,
  `language` text NOT NULL,
  `title` text NULL,
  `overview` text NULL,
  `tagline` text NULL,
  `poster_asset` text NULL,
  `backdrop_asset` text NULL,
  `enriched_at` datetime NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`movie_id`, `language`),
  CONSTRAINT `movie_i18n_movie_id_fkey` FOREIGN KEY (`movie_id`) REFERENCES `movies` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create "movie_states" table
CREATE TABLE `movie_states` (
  `instance_name` text NOT NULL,
  `radarr_movie_id` integer NOT NULL,
  `movie_id` integer NOT NULL,
  `title_slug` text NOT NULL,
  `monitored` boolean NOT NULL DEFAULT false,
  `has_file` boolean NOT NULL DEFAULT false,
  `availability` text NULL,
  `size_on_disk_bytes` integer NOT NULL DEFAULT 0,
  `added_to_radarr` boolean NOT NULL DEFAULT false,
  `updated_at` datetime NOT NULL,
  `deleted_at` datetime NULL,
  PRIMARY KEY (`instance_name`, `radarr_movie_id`),
  CONSTRAINT `movie_states_movie_id_fkey` FOREIGN KEY (`movie_id`) REFERENCES `movies` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
CREATE INDEX `movie_states_instance_active` ON `movie_states` (`instance_name`) WHERE deleted_at IS NULL;
CREATE INDEX `movie_states_movie_id` ON `movie_states` (`movie_id`);
-- create "collections" table
CREATE TABLE `collections` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_collection_id` integer NOT NULL,
  `name` text NOT NULL,
  `overview` text NULL,
  `poster_asset` text NULL,
  `backdrop_asset` text NULL,
  `monitored` boolean NOT NULL DEFAULT false,
  `radarr_monitored` boolean NOT NULL DEFAULT false,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
CREATE UNIQUE INDEX `collections_tmdb_collection_id` ON `collections` (`tmdb_collection_id`);
