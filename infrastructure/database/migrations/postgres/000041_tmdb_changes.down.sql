-- reverse: drop "tmdb_changes_state", series_tmdb_changed_idx, series.tmdb_changed_at
DROP TABLE "tmdb_changes_state";
DROP INDEX "series_tmdb_changed_idx";
ALTER TABLE "series" DROP COLUMN "tmdb_changed_at";
