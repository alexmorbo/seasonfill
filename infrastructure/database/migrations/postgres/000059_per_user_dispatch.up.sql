-- Ф8-U-5c: per-user notification dispatch. Adds user_id FK→users CASCADE to
-- notification_outbox (target follower) and notified_events (per-user dedup),
-- reshaping notified_events PK to (user_id, event_type, entity_key). Existing
-- rows belonged to the single implicit pre-RBAC user → backfilled to the seed
-- admin (lowest-id role='admin'). Proven by
-- internal/shared/db/migration_000059_roundtrip_test.go.

-- notification_outbox -> per-user (surrogate PK id stays)
ALTER TABLE "notification_outbox" ADD COLUMN "user_id" bigint NULL;
-- atlas:nolint
UPDATE "notification_outbox" SET "user_id" =
  (SELECT "id" FROM "users" WHERE "role" = 'admin' ORDER BY "id" ASC LIMIT 1)
  WHERE "user_id" IS NULL;
-- atlas:nolint
ALTER TABLE "notification_outbox" ALTER COLUMN "user_id" SET NOT NULL;
ALTER TABLE "notification_outbox" ADD CONSTRAINT "notification_outbox_user_id_fkey"
  FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- per-user due-batch scan index (MAIN pre-decision; query-optional, see §3 note)
CREATE INDEX "notification_outbox_user_pending"
  ON "notification_outbox" ("user_id", "status", "next_attempt_at");

-- notified_events -> per-user dedup (PK becomes (user_id, event_type, entity_key))
ALTER TABLE "notified_events" ADD COLUMN "user_id" bigint NULL;
-- atlas:nolint
UPDATE "notified_events" SET "user_id" =
  (SELECT "id" FROM "users" WHERE "role" = 'admin' ORDER BY "id" ASC LIMIT 1)
  WHERE "user_id" IS NULL;
-- atlas:nolint
ALTER TABLE "notified_events" ALTER COLUMN "user_id" SET NOT NULL;
-- atlas:nolint
ALTER TABLE "notified_events" DROP CONSTRAINT "notified_events_pkey";
ALTER TABLE "notified_events" ADD PRIMARY KEY ("user_id", "event_type", "entity_key");
ALTER TABLE "notified_events" ADD CONSTRAINT "notified_events_user_id_fkey"
  FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
