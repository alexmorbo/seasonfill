-- create "people" table
CREATE TABLE `people` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_id` integer NULL,
  `imdb_id` text NULL,
  `hydration` text NOT NULL DEFAULT 'stub',
  `name` text NOT NULL,
  `original_name` text NULL,
  `gender` integer NULL,
  `birthday` date NULL,
  `deathday` date NULL,
  `place_of_birth` text NULL,
  `known_for_department` text NULL,
  `popularity` double precision NULL,
  `profile_asset` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "people_tmdb_id" to table: "people"
CREATE UNIQUE INDEX `people_tmdb_id` ON `people` (`tmdb_id`) WHERE tmdb_id IS NOT NULL;
-- create index "people_imdb_id" to table: "people"
CREATE INDEX `people_imdb_id` ON `people` (`imdb_id`);
-- create "person_credits" table
CREATE TABLE `person_credits` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `person_id` integer NOT NULL,
  `tmdb_credit_id` text NOT NULL,
  `media_type` text NOT NULL,
  `tmdb_media_id` integer NOT NULL,
  `title` text NOT NULL,
  `original_title` text NULL,
  `year` integer NULL,
  `character_name` text NULL,
  `kind` text NOT NULL,
  `department` text NULL,
  `job` text NULL,
  `poster_path` text NULL,
  `vote_average` double precision NULL,
  `tmdb_votes` integer NULL,
  `episode_count` integer NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `person_credits_person_id_fkey` FOREIGN KEY (`person_id`) REFERENCES `people` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "person_credits_credit" to table: "person_credits"
CREATE UNIQUE INDEX `person_credits_credit` ON `person_credits` (`person_id`, `tmdb_credit_id`);
-- create index "person_credits_media" to table: "person_credits"
CREATE INDEX `person_credits_media` ON `person_credits` (`media_type`, `tmdb_media_id`);
-- create index "person_credits_person" to table: "person_credits"
CREATE INDEX `person_credits_person` ON `person_credits` (`person_id`);
-- create "person_biographies" table
CREATE TABLE `person_biographies` (
  `person_id` integer NOT NULL,
  `language` text NOT NULL,
  `biography` text NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`person_id`, `language`),
  CONSTRAINT `person_biographies_person_id_fkey` FOREIGN KEY (`person_id`) REFERENCES `people` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
