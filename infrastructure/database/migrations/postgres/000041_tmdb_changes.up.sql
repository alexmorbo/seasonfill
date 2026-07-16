-- W2-1: TMDB /tv/changes firehose foundation. Adds the write-once
-- series.tmdb_changed_at clock (+ partial index) and the single-row
-- tmdb_changes_state poller cursor. No behavior yet — schema only.

-- modify "series" table: add column "tmdb_changed_at"
ALTER TABLE "series" ADD COLUMN "tmdb_changed_at" timestamptz NULL;
-- create index "series_tmdb_changed_idx" to table: "series"
CREATE INDEX "series_tmdb_changed_idx" ON "series" ("tmdb_changed_at") WHERE (tmdb_changed_at IS NOT NULL);
-- create "tmdb_changes_state" table
CREATE TABLE "tmdb_changes_state" (
  "id" bigint NOT NULL,
  "schema_version" integer NOT NULL DEFAULT 1,
  "last_window_end" timestamptz NULL,
  "last_poll_at" timestamptz NULL,
  "last_matched" integer NOT NULL DEFAULT 0,
  "last_firehose" integer NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "tmdb_changes_state_single" CHECK (id = 1)
);
