-- create "followed_series" table
CREATE TABLE "followed_series" (
  "series_id" bigint NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("series_id"),
  CONSTRAINT "followed_series_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
