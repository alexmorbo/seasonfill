-- reverse: modify "movie_states" table
ALTER TABLE "movie_states" DROP COLUMN "audio_codec", DROP COLUMN "video_codec", DROP COLUMN "resolution", DROP COLUMN "quality";
