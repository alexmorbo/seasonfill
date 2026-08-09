-- reverse: create index "notification_outbox_pending" to table: "notification_outbox"
DROP INDEX "notification_outbox_pending";
-- reverse: create index "notification_outbox_dedup" to table: "notification_outbox"
DROP INDEX "notification_outbox_dedup";
-- reverse: create "notification_outbox" table
DROP TABLE "notification_outbox";
-- reverse: create "notification_agents" table
DROP TABLE "notification_agents";
