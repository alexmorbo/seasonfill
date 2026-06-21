-- create "series_images" table
CREATE TABLE "series_images" (
  "id" bigserial NOT NULL,
  "series_id" bigint NOT NULL,
  "language" text NOT NULL,
  "kind" text NOT NULL,
  "tmdb_path" text NOT NULL,
  "asset_hash" text NULL,
  "iso_lang" text NULL,
  "vote_average" double precision NULL,
  "vote_count" integer NULL,
  "width" integer NULL,
  "height" integer NULL,
  "position" integer NOT NULL DEFAULT 0,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "series_images_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "series_images_series_kind_position" to table: "series_images"
CREATE INDEX "series_images_series_kind_position" ON "series_images" ("series_id", "kind", "position");
-- create index "series_images_series_lang_kind_position" to table: "series_images"
CREATE UNIQUE INDEX "series_images_series_lang_kind_position" ON "series_images" ("series_id", "language", "kind", "position");
