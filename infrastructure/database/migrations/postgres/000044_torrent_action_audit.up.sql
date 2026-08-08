-- create "torrent_action_audit" table
CREATE TABLE "torrent_action_audit" (
  "id" bigserial NOT NULL,
  "instance_name" text NOT NULL,
  "hash" text NOT NULL,
  "action" text NOT NULL,
  "actor" text NOT NULL,
  "result" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- create index "torrent_action_audit_hash_created_idx" to table: "torrent_action_audit"
CREATE INDEX "torrent_action_audit_hash_created_idx" ON "torrent_action_audit" ("hash", "created_at");
