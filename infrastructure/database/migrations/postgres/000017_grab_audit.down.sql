-- reverse: create "origin_releases" table
DROP TABLE "origin_releases";
-- reverse: create index "decisions_scan_run_idx" to table: "decisions"
DROP INDEX "decisions_scan_run_idx";
-- reverse: create index "decisions_instance_series_idx" to table: "decisions"
DROP INDEX "decisions_instance_series_idx";
-- reverse: create index "decisions_created_at_id_idx" to table: "decisions"
DROP INDEX "decisions_created_at_id_idx";
-- reverse: create "decisions" table
DROP TABLE "decisions";
-- reverse: create index "cooldowns_expires_at_idx" to table: "cooldowns"
DROP INDEX "cooldowns_expires_at_idx";
-- reverse: create "cooldowns" table
DROP TABLE "cooldowns";
