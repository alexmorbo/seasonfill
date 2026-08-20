-- add column "quality" to table: "movie_states"
ALTER TABLE `movie_states` ADD COLUMN `quality` text NULL;
-- add column "resolution" to table: "movie_states"
ALTER TABLE `movie_states` ADD COLUMN `resolution` integer NULL;
-- add column "video_codec" to table: "movie_states"
ALTER TABLE `movie_states` ADD COLUMN `video_codec` text NULL;
-- add column "audio_codec" to table: "movie_states"
ALTER TABLE `movie_states` ADD COLUMN `audio_codec` text NULL;
