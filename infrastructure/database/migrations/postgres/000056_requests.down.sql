-- reverse: create index "requests_user_id_idx" to table: "requests"
DROP INDEX "requests_user_id_idx";
-- reverse: create index "requests_status_idx" to table: "requests"
DROP INDEX "requests_status_idx";
-- reverse: create index "requests_pending_uniq" to table: "requests"
DROP INDEX "requests_pending_uniq";
-- reverse: create "requests" table
DROP TABLE "requests";
