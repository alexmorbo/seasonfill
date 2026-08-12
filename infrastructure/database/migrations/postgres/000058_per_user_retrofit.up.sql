-- Ф8-U-5: retrofit user_id into discovery_blocklist, followed_series,
-- notification_agents so multi-user RBAC is per-user. Adds user_id FK→users
-- CASCADE, backfills existing rows to the seed admin (lowest-id role='admin'),
-- reshapes per-user unique/primary keys. Proven by
-- internal/shared/db/migration_000058_roundtrip_test.go.

-- discovery_blocklist -> per-user (surrogate PK id stays; UNIQUE becomes per-user)
ALTER TABLE "discovery_blocklist" ADD COLUMN "user_id" bigint NULL;
-- atlas:nolint
UPDATE "discovery_blocklist" SET "user_id" =
  (SELECT "id" FROM "users" WHERE "role" = 'admin' ORDER BY "id" ASC LIMIT 1)
  WHERE "user_id" IS NULL;
-- atlas:nolint
ALTER TABLE "discovery_blocklist" ALTER COLUMN "user_id" SET NOT NULL;
ALTER TABLE "discovery_blocklist" ADD CONSTRAINT "discovery_blocklist_user_id_fkey"
  FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- atlas:nolint
DROP INDEX "discovery_blocklist_kind_ref";
-- atlas:nolint MF101
CREATE UNIQUE INDEX "discovery_blocklist_kind_ref"
  ON "discovery_blocklist" ("user_id", "kind", "ref_id");

-- followed_series -> per-user (composite PK (user_id, series_id))
ALTER TABLE "followed_series" ADD COLUMN "user_id" bigint NULL;
-- atlas:nolint
UPDATE "followed_series" SET "user_id" =
  (SELECT "id" FROM "users" WHERE "role" = 'admin' ORDER BY "id" ASC LIMIT 1)
  WHERE "user_id" IS NULL;
-- atlas:nolint
ALTER TABLE "followed_series" ALTER COLUMN "user_id" SET NOT NULL;
-- atlas:nolint
ALTER TABLE "followed_series" DROP CONSTRAINT "followed_series_pkey";
ALTER TABLE "followed_series" ADD PRIMARY KEY ("user_id", "series_id");
ALTER TABLE "followed_series" ADD CONSTRAINT "followed_series_user_id_fkey"
  FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;

-- notification_agents -> per-user (surrogate PK id stays)
ALTER TABLE "notification_agents" ADD COLUMN "user_id" bigint NULL;
-- atlas:nolint
UPDATE "notification_agents" SET "user_id" =
  (SELECT "id" FROM "users" WHERE "role" = 'admin' ORDER BY "id" ASC LIMIT 1)
  WHERE "user_id" IS NULL;
-- atlas:nolint
ALTER TABLE "notification_agents" ALTER COLUMN "user_id" SET NOT NULL;
ALTER TABLE "notification_agents" ADD CONSTRAINT "notification_agents_user_id_fkey"
  FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
