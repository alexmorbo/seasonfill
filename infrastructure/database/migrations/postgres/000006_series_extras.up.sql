-- create "external_ids" table
CREATE TABLE "external_ids" (
  "entity_type" text NOT NULL,
  "entity_id" bigint NOT NULL,
  "provider" text NOT NULL,
  "value" text NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("entity_type", "entity_id", "provider")
);
-- create index "external_ids_provider_value" to table: "external_ids"
CREATE INDEX "external_ids_provider_value" ON "external_ids" ("provider", "value");
-- create "content_ratings" table
CREATE TABLE "content_ratings" (
  "series_id" bigint NOT NULL,
  "country_code" text NOT NULL,
  "rating" text NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("series_id", "country_code"),
  CONSTRAINT "content_ratings_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create "series_recommendations" table
CREATE TABLE "series_recommendations" (
  "series_id" bigint NOT NULL,
  "recommended_series_id" bigint NOT NULL,
  "position" integer NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("series_id", "recommended_series_id"),
  CONSTRAINT "series_recommendations_recommended_series_id_fkey" FOREIGN KEY ("recommended_series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "series_recommendations_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "series_recommendations_position" to table: "series_recommendations"
CREATE INDEX "series_recommendations_position" ON "series_recommendations" ("series_id", "position");
-- create "videos" table
CREATE TABLE "videos" (
  "id" bigserial NOT NULL,
  "series_id" bigint NOT NULL,
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
  CONSTRAINT "videos_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "videos_series_type" to table: "videos"
CREATE INDEX "videos_series_type" ON "videos" ("series_id", "type", "official");
-- create index "videos_tmdb_id" to table: "videos"
CREATE UNIQUE INDEX "videos_tmdb_id" ON "videos" ("tmdb_video_id") WHERE (tmdb_video_id IS NOT NULL);
