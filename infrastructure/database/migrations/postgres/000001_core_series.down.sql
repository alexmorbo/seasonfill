-- reverse: create index "episodes_natural" to table: "episodes"
DROP INDEX "episodes_natural";
-- reverse: create index "episodes_air_date" to table: "episodes"
DROP INDEX "episodes_air_date";
-- reverse: create "episodes" table
DROP TABLE "episodes";
-- reverse: create index "seasons_natural" to table: "seasons"
DROP INDEX "seasons_natural";
-- reverse: create "seasons" table
DROP TABLE "seasons";
-- reverse: create index "series_tvdb_id_idx" to table: "series"
DROP INDEX "series_tvdb_id_idx";
-- reverse: create index "series_tmdb_type_idx" to table: "series"
DROP INDEX "series_tmdb_type_idx";
-- reverse: create index "series_tmdb_rating_idx" to table: "series"
DROP INDEX "series_tmdb_rating_idx";
-- reverse: create index "series_tmdb_id_idx" to table: "series"
DROP INDEX "series_tmdb_id_idx";
-- reverse: create index "series_popularity_idx" to table: "series"
DROP INDEX "series_popularity_idx";
-- reverse: create index "series_imdb_id_idx" to table: "series"
DROP INDEX "series_imdb_id_idx";
-- reverse: create "series" table
DROP TABLE "series";
