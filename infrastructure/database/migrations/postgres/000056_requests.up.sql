-- create "requests" table
CREATE TABLE "requests" (
  "id" bigserial NOT NULL,
  "user_id" bigint NOT NULL,
  "media_type" text NOT NULL,
  "tmdb_id" bigint NOT NULL,
  "seasons" jsonb NULL,
  "payload" jsonb NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "approver_id" bigint NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "requests_approver_id_fkey" FOREIGN KEY ("approver_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "requests_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "requests_pending_uniq" to table: "requests"
CREATE UNIQUE INDEX "requests_pending_uniq" ON "requests" ("user_id", "media_type", "tmdb_id") WHERE (status = 'pending'::text);
-- create index "requests_status_idx" to table: "requests"
CREATE INDEX "requests_status_idx" ON "requests" ("status");
-- create index "requests_user_id_idx" to table: "requests"
CREATE INDEX "requests_user_id_idx" ON "requests" ("user_id");
-- Ф8-U-2 admin-perms backfill: pre-RBAC admins got perms=false from mig-055 default
-- (seed-admin only stamps perms on CREATE). Without this an existing admin's add
-- routes to the QUEUE instead of direct-add.
UPDATE "users" SET "auto_approve" = true, "request" = true, "manage_requests" = true, "manage_users" = true, "request_4k" = true WHERE "role" = 'admin';
