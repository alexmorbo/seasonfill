-- create "episode_texts" table
CREATE TABLE "episode_texts" (
  "episode_id" bigint NOT NULL,
  "language" text NOT NULL,
  "title" text NULL,
  "overview" text NULL,
  "enriched_at" timestamptz NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("episode_id", "language"),
  CONSTRAINT "episode_texts_episode_id_fkey" FOREIGN KEY ("episode_id") REFERENCES "episodes" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create "series_texts" table
CREATE TABLE "series_texts" (
  "series_id" bigint NOT NULL,
  "language" text NOT NULL,
  "title" text NULL,
  "overview" text NULL,
  "tagline" text NULL,
  "enriched_at" timestamptz NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("series_id", "language"),
  CONSTRAINT "series_texts_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
