-- create "movie_companies" table
CREATE TABLE "movie_companies" (
  "movie_id" bigint NOT NULL,
  "company_id" bigint NOT NULL,
  "position" integer NULL,
  PRIMARY KEY ("movie_id", "company_id"),
  CONSTRAINT "movie_companies_company_id_fkey" FOREIGN KEY ("company_id") REFERENCES "production_companies" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "movie_companies_movie_id_fkey" FOREIGN KEY ("movie_id") REFERENCES "movies" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "movie_companies_company" to table: "movie_companies"
CREATE INDEX "movie_companies_company" ON "movie_companies" ("company_id");
-- create "movie_genres" table
CREATE TABLE "movie_genres" (
  "movie_id" bigint NOT NULL,
  "genre_id" bigint NOT NULL,
  "position" integer NULL,
  PRIMARY KEY ("movie_id", "genre_id"),
  CONSTRAINT "movie_genres_genre_id_fkey" FOREIGN KEY ("genre_id") REFERENCES "genres" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "movie_genres_movie_id_fkey" FOREIGN KEY ("movie_id") REFERENCES "movies" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "movie_genres_genre" to table: "movie_genres"
CREATE INDEX "movie_genres_genre" ON "movie_genres" ("genre_id");
-- create "movie_keywords" table
CREATE TABLE "movie_keywords" (
  "movie_id" bigint NOT NULL,
  "keyword_id" bigint NOT NULL,
  PRIMARY KEY ("movie_id", "keyword_id"),
  CONSTRAINT "movie_keywords_keyword_id_fkey" FOREIGN KEY ("keyword_id") REFERENCES "keywords" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "movie_keywords_movie_id_fkey" FOREIGN KEY ("movie_id") REFERENCES "movies" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "movie_keywords_keyword" to table: "movie_keywords"
CREATE INDEX "movie_keywords_keyword" ON "movie_keywords" ("keyword_id");
-- create "movie_recommendations" table
CREATE TABLE "movie_recommendations" (
  "movie_id" bigint NOT NULL,
  "recommended_movie_id" bigint NOT NULL,
  "position" integer NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("movie_id", "recommended_movie_id"),
  CONSTRAINT "movie_recommendations_movie_id_fkey" FOREIGN KEY ("movie_id") REFERENCES "movies" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "movie_recommendations_recommended_movie_id_fkey" FOREIGN KEY ("recommended_movie_id") REFERENCES "movies" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "movie_recommendations_no_self_ref" CHECK (recommended_movie_id <> movie_id)
);
-- create index "movie_recommendations_position" to table: "movie_recommendations"
CREATE INDEX "movie_recommendations_position" ON "movie_recommendations" ("movie_id", "position");
-- create "movie_videos" table
CREATE TABLE "movie_videos" (
  "id" bigserial NOT NULL,
  "movie_id" bigint NOT NULL,
  "tmdb_video_id" text NULL,
  "name" text NOT NULL,
  "site" text NULL,
  "key" text NULL,
  "type" text NULL,
  "official" boolean NOT NULL DEFAULT false,
  "language" text NULL,
  "published_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "movie_videos_movie_id_fkey" FOREIGN KEY ("movie_id") REFERENCES "movies" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "movie_videos_movie_type" to table: "movie_videos"
CREATE INDEX "movie_videos_movie_type" ON "movie_videos" ("movie_id", "type", "official");
-- create index "movie_videos_tmdb_id" to table: "movie_videos"
CREATE UNIQUE INDEX "movie_videos_tmdb_id" ON "movie_videos" ("tmdb_video_id") WHERE (tmdb_video_id IS NOT NULL);
