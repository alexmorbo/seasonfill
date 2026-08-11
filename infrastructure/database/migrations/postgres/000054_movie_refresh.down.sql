-- reverse: create "movie_changes_state" table
DROP TABLE "movie_changes_state";
-- reverse: create index "movies_tmdb_changed_at_idx" to table: "movies"
DROP INDEX "movies_tmdb_changed_at_idx";
-- reverse: modify "movies" table
ALTER TABLE "movies" DROP COLUMN "tmdb_changed_at";
