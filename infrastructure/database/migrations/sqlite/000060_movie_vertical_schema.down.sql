-- reverse: create index "movie_videos_movie_type" to table: "movie_videos"
DROP INDEX `movie_videos_movie_type`;
-- reverse: create index "movie_videos_tmdb_id" to table: "movie_videos"
DROP INDEX `movie_videos_tmdb_id`;
-- reverse: create "movie_videos" table
DROP TABLE `movie_videos`;
-- reverse: create index "movie_recommendations_position" to table: "movie_recommendations"
DROP INDEX `movie_recommendations_position`;
-- reverse: create "movie_recommendations" table
DROP TABLE `movie_recommendations`;
-- reverse: create index "movie_keywords_keyword" to table: "movie_keywords"
DROP INDEX `movie_keywords_keyword`;
-- reverse: create "movie_keywords" table
DROP TABLE `movie_keywords`;
-- reverse: create index "movie_companies_company" to table: "movie_companies"
DROP INDEX `movie_companies_company`;
-- reverse: create "movie_companies" table
DROP TABLE `movie_companies`;
-- reverse: create index "movie_genres_genre" to table: "movie_genres"
DROP INDEX `movie_genres_genre`;
-- reverse: create "movie_genres" table
DROP TABLE `movie_genres`;
