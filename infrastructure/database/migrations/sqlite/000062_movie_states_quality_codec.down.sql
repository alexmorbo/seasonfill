-- reverse: add column "audio_codec" to table: "movie_states"
ALTER TABLE `movie_states` DROP COLUMN `audio_codec`;
-- reverse: add column "video_codec" to table: "movie_states"
ALTER TABLE `movie_states` DROP COLUMN `video_codec`;
-- reverse: add column "resolution" to table: "movie_states"
ALTER TABLE `movie_states` DROP COLUMN `resolution`;
-- reverse: add column "quality" to table: "movie_states"
ALTER TABLE `movie_states` DROP COLUMN `quality`;
