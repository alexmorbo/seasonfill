-- reverse: modify "grab_records" table
ALTER TABLE "grab_records" DROP CONSTRAINT "grab_records_scan_run_id_fkey";
-- reverse: create index "idx_scan_runs_started_at_id" to table: "scan_runs"
DROP INDEX "idx_scan_runs_started_at_id";
-- reverse: create index "idx_scan_runs_instance_name" to table: "scan_runs"
DROP INDEX "idx_scan_runs_instance_name";
-- reverse: create index "idx_scan_runs_created_at_id" to table: "scan_runs"
DROP INDEX "idx_scan_runs_created_at_id";
-- reverse: create "scan_runs" table
DROP TABLE "scan_runs";
