-- create "scan_runs" table
CREATE TABLE "scan_runs" (
  "id" text NOT NULL,
  "instance_name" text NOT NULL,
  "trigger" text NOT NULL,
  "started_at" timestamptz NOT NULL,
  "finished_at" timestamptz NULL,
  "status" text NOT NULL DEFAULT 'running',
  "series_scanned" integer NOT NULL DEFAULT 0,
  "candidates_found" integer NOT NULL DEFAULT 0,
  "grabs_performed" integer NOT NULL DEFAULT 0,
  "grabs_failed" integer NOT NULL DEFAULT 0,
  "errors_count" integer NOT NULL DEFAULT 0,
  "error_message" text NOT NULL DEFAULT '',
  "dry_run" boolean NOT NULL DEFAULT false,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- create index "idx_scan_runs_created_at_id" to table: "scan_runs"
CREATE INDEX "idx_scan_runs_created_at_id" ON "scan_runs" ("created_at", "id");
-- create index "idx_scan_runs_instance_name" to table: "scan_runs"
CREATE INDEX "idx_scan_runs_instance_name" ON "scan_runs" ("instance_name");
-- create index "idx_scan_runs_started_at_id" to table: "scan_runs"
CREATE INDEX "idx_scan_runs_started_at_id" ON "scan_runs" ("started_at", "id");
-- modify "grab_records" table
ALTER TABLE "grab_records" ADD CONSTRAINT "grab_records_scan_run_id_fkey" FOREIGN KEY ("scan_run_id") REFERENCES "scan_runs" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
