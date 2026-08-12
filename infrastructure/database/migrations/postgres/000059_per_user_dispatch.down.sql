-- reverse: notified_events (restore (event_type, entity_key) PK)
ALTER TABLE "notified_events" DROP CONSTRAINT "notified_events_user_id_fkey";
ALTER TABLE "notified_events" DROP CONSTRAINT "notified_events_pkey";
ALTER TABLE "notified_events" ADD PRIMARY KEY ("event_type", "entity_key");
ALTER TABLE "notified_events" DROP COLUMN "user_id";

-- reverse: notification_outbox
DROP INDEX "notification_outbox_user_pending";
ALTER TABLE "notification_outbox" DROP CONSTRAINT "notification_outbox_user_id_fkey";
ALTER TABLE "notification_outbox" DROP COLUMN "user_id";
