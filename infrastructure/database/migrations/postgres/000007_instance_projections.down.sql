-- reverse: create index "series_cache_series_id" to table: "series_cache"
DROP INDEX "series_cache_series_id";
-- reverse: create index "series_cache_instance_active" to table: "series_cache"
DROP INDEX "series_cache_instance_active";
-- reverse: create "series_cache" table
DROP TABLE "series_cache";
-- reverse: create index "episode_states_deleted_at" to table: "episode_states"
DROP INDEX "episode_states_deleted_at";
-- reverse: create "episode_states" table
DROP TABLE "episode_states";
-- reverse: create index "season_stats_series" to table: "season_stats"
DROP INDEX "season_stats_series";
-- reverse: create "season_stats" table
DROP TABLE "season_stats";
