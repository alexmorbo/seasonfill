-- modify "movie_states" table
ALTER TABLE "movie_states" ADD COLUMN "quality" text NULL, ADD COLUMN "resolution" integer NULL, ADD COLUMN "video_codec" text NULL, ADD COLUMN "audio_codec" text NULL;
