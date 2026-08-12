-- reverse: notification_agents
ALTER TABLE "notification_agents" DROP CONSTRAINT "notification_agents_user_id_fkey";
ALTER TABLE "notification_agents" DROP COLUMN "user_id";

-- reverse: followed_series (restore single-column PK)
ALTER TABLE "followed_series" DROP CONSTRAINT "followed_series_user_id_fkey";
ALTER TABLE "followed_series" DROP CONSTRAINT "followed_series_pkey";
ALTER TABLE "followed_series" ADD PRIMARY KEY ("series_id");
ALTER TABLE "followed_series" DROP COLUMN "user_id";

-- reverse: discovery_blocklist (restore global UNIQUE)
DROP INDEX "discovery_blocklist_kind_ref";
ALTER TABLE "discovery_blocklist" DROP CONSTRAINT "discovery_blocklist_user_id_fkey";
ALTER TABLE "discovery_blocklist" DROP COLUMN "user_id";
CREATE UNIQUE INDEX "discovery_blocklist_kind_ref" ON "discovery_blocklist" ("kind", "ref_id");
