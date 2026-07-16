-- reverse: create "tmdb_changes_state" table
DROP TABLE `tmdb_changes_state`;
-- reverse: create index "series_tmdb_changed_idx" to table: "series"
DROP INDEX `series_tmdb_changed_idx`;
-- reverse: add column "tmdb_changed_at" to table: "series"
ALTER TABLE `series` DROP COLUMN `tmdb_changed_at`;
