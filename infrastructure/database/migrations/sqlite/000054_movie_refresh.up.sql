-- add column "tmdb_changed_at" to table: "movies"
ALTER TABLE `movies` ADD COLUMN `tmdb_changed_at` datetime NULL;
-- create index "movies_tmdb_changed_at_idx" to table: "movies"
CREATE INDEX `movies_tmdb_changed_at_idx` ON `movies` (`tmdb_changed_at`) WHERE tmdb_changed_at IS NOT NULL;
-- create "movie_changes_state" table
CREATE TABLE `movie_changes_state` (
  `id` integer NOT NULL,
  `schema_version` integer NOT NULL DEFAULT 1,
  `last_window_end` datetime NULL,
  `last_poll_at` datetime NULL,
  `last_matched` integer NOT NULL DEFAULT 0,
  `last_firehose` integer NOT NULL DEFAULT 0,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`id`),
  CONSTRAINT `movie_changes_state_single` CHECK (id = 1)
);
