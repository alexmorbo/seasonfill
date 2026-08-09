-- create "notification_agents" table
CREATE TABLE "notification_agents" (
  "id" bigserial NOT NULL,
  "name" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT false,
  "config_encrypted" bytea NOT NULL,
  "event_types" jsonb NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- create "notification_outbox" table
CREATE TABLE "notification_outbox" (
  "id" bigserial NOT NULL,
  "event_type" text NOT NULL,
  "payload" jsonb NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "attempts" integer NOT NULL DEFAULT 0,
  "next_attempt_at" timestamptz NULL,
  "dedup_key" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- create index "notification_outbox_dedup" to table: "notification_outbox"
CREATE INDEX "notification_outbox_dedup" ON "notification_outbox" ("dedup_key") WHERE (status = 'pending'::text);
-- create index "notification_outbox_pending" to table: "notification_outbox"
CREATE INDEX "notification_outbox_pending" ON "notification_outbox" ("next_attempt_at") WHERE (status = 'pending'::text);
