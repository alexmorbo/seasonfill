-- Story 1084 (S-1084b) — drop people.name. The localized display name moved
-- to people_texts (000035); the language-neutral fallback is
-- people.original_name. All readers repointed + coverage-verified in 1084a.
-- Pre-drop backfill (DF-4): preserve the last-writer base tier into
-- original_name where it is missing. Must run BEFORE the table rebuild,
-- while `name` still exists.
UPDATE `people` SET `original_name` = `name` WHERE `original_name` IS NULL OR `original_name` = '';
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_people" table
-- atlas:nolint destructive
CREATE TABLE `new_people` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_id` integer NULL,
  `imdb_id` text NULL,
  `hydration` text NOT NULL DEFAULT 'stub',
  `original_name` text NULL,
  `gender` integer NULL,
  `birthday` date NULL,
  `deathday` date NULL,
  `place_of_birth` text NULL,
  `known_for_department` text NULL,
  `popularity` double precision NULL,
  `profile_asset` text NULL,
  `enrichment_synced_at` datetime NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- copy rows from old table "people" to new temporary table "new_people"
INSERT INTO `new_people` (`id`, `tmdb_id`, `imdb_id`, `hydration`, `original_name`, `gender`, `birthday`, `deathday`, `place_of_birth`, `known_for_department`, `popularity`, `profile_asset`, `enrichment_synced_at`, `created_at`, `updated_at`) SELECT `id`, `tmdb_id`, `imdb_id`, `hydration`, `original_name`, `gender`, `birthday`, `deathday`, `place_of_birth`, `known_for_department`, `popularity`, `profile_asset`, `enrichment_synced_at`, `created_at`, `updated_at` FROM `people`;
-- drop "people" table after copying rows
DROP TABLE `people`;
-- rename temporary table "new_people" to "people"
ALTER TABLE `new_people` RENAME TO `people`;
-- create index "people_tmdb_id" to table: "people"
CREATE UNIQUE INDEX `people_tmdb_id` ON `people` (`tmdb_id`) WHERE tmdb_id IS NOT NULL;
-- create index "people_imdb_id" to table: "people"
CREATE INDEX `people_imdb_id` ON `people` (`imdb_id`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
