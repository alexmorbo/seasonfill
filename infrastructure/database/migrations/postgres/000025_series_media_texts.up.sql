-- create "series_media_texts" table
CREATE TABLE "series_media_texts" (
  "series_id" bigint NOT NULL,
  "language" text NOT NULL,
  "poster_asset" text NULL,
  "poster_hash" text NULL,
  "backdrop_asset" text NULL,
  "backdrop_hash" text NULL,
  "enriched_at" timestamptz NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("series_id", "language"),
  CONSTRAINT "series_media_texts_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
