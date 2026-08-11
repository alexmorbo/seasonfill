-- modify "movies" table
ALTER TABLE "movies" ADD COLUMN "tmdb_changed_at" timestamptz NULL;
-- create index "movies_tmdb_changed_at_idx" to table: "movies"
CREATE INDEX "movies_tmdb_changed_at_idx" ON "movies" ("tmdb_changed_at") WHERE (tmdb_changed_at IS NOT NULL);
-- create "movie_changes_state" table
CREATE TABLE "movie_changes_state" (
  "id" bigint NOT NULL,
  "schema_version" integer NOT NULL DEFAULT 1,
  "last_window_end" timestamptz NULL,
  "last_poll_at" timestamptz NULL,
  "last_matched" integer NOT NULL DEFAULT 0,
  "last_firehose" integer NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "movie_changes_state_single" CHECK (id = 1)
);
