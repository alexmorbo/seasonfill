-- create "cooldowns" table
CREATE TABLE "cooldowns" (
  "scope" text NOT NULL,
  "key" text NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "reason" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("scope", "key")
);
-- create index "cooldowns_expires_at_idx" to table: "cooldowns"
CREATE INDEX "cooldowns_expires_at_idx" ON "cooldowns" ("expires_at");
-- create "decisions" table
CREATE TABLE "decisions" (
  "id" text NOT NULL,
  "scan_run_id" text NULL,
  "instance_name" text NOT NULL,
  "series_id" bigint NOT NULL,
  "series_title" text NULL,
  "season_number" integer NOT NULL,
  "decision" text NOT NULL,
  "reason" text NULL,
  "missing_count" integer NOT NULL DEFAULT 0,
  "existing_count" integer NOT NULL DEFAULT 0,
  "releases_found" integer NOT NULL DEFAULT 0,
  "candidates_count" integer NOT NULL DEFAULT 0,
  "filtered_out" jsonb NULL,
  "selected_guid" text NULL,
  "selected_data" jsonb NULL,
  "would_grab" boolean NOT NULL DEFAULT false,
  "error_detail" text NULL,
  "superseded_by_id" text NULL,
  "total_episodes" integer NOT NULL DEFAULT 0,
  "aired_episodes" integer NOT NULL DEFAULT 0,
  "existing_episodes" integer NOT NULL DEFAULT 0,
  "grabbed_episodes" integer NOT NULL DEFAULT 0,
  "intent" jsonb NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "decisions_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "sonarr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "decisions_scan_run_id_fkey" FOREIGN KEY ("scan_run_id") REFERENCES "scan_runs" ("id") ON UPDATE NO ACTION ON DELETE SET NULL
);
-- create index "decisions_created_at_id_idx" to table: "decisions"
CREATE INDEX "decisions_created_at_id_idx" ON "decisions" ("created_at" DESC, "id" DESC);
-- create index "decisions_instance_series_idx" to table: "decisions"
CREATE INDEX "decisions_instance_series_idx" ON "decisions" ("instance_name", "series_id", "season_number");
-- create index "decisions_scan_run_idx" to table: "decisions"
CREATE INDEX "decisions_scan_run_idx" ON "decisions" ("scan_run_id");
-- create "origin_releases" table
CREATE TABLE "origin_releases" (
  "instance_name" text NOT NULL,
  "series_id" bigint NOT NULL,
  "season_number" integer NOT NULL,
  "guid" text NOT NULL,
  "indexer_id" integer NOT NULL DEFAULT 0,
  "indexer_name" text NULL,
  "source" text NOT NULL,
  "first_seen_at" timestamptz NOT NULL DEFAULT now(),
  "last_seen_at" timestamptz NOT NULL DEFAULT now(),
  "last_used_at" timestamptz NULL,
  PRIMARY KEY ("instance_name", "series_id", "season_number"),
  CONSTRAINT "origin_releases_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "sonarr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE
);
