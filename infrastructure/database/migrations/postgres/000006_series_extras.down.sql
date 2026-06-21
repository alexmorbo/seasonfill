-- reverse: create index "videos_tmdb_id" to table: "videos"
DROP INDEX "videos_tmdb_id";
-- reverse: create index "videos_series_type" to table: "videos"
DROP INDEX "videos_series_type";
-- reverse: create "videos" table
DROP TABLE "videos";
-- reverse: create index "series_recommendations_position" to table: "series_recommendations"
DROP INDEX "series_recommendations_position";
-- reverse: create "series_recommendations" table
DROP TABLE "series_recommendations";
-- reverse: create "content_ratings" table
DROP TABLE "content_ratings";
-- reverse: create index "external_ids_provider_value" to table: "external_ids"
DROP INDEX "external_ids_provider_value";
-- reverse: create "external_ids" table
DROP TABLE "external_ids";
