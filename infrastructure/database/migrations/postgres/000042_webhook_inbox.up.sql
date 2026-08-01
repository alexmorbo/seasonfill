-- create "webhook_inbox" table
CREATE TABLE "webhook_inbox" (
  "id" bigserial NOT NULL,
  "instance_name" text NOT NULL,
  "event_type" text NOT NULL,
  "payload" jsonb NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "attempts" integer NOT NULL DEFAULT 0,
  "next_attempt_at" timestamptz NULL,
  "lease_until" timestamptz NULL,
  "last_error" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- create index "webhook_inbox_pending" to table: "webhook_inbox"
CREATE INDEX "webhook_inbox_pending" ON "webhook_inbox" ("next_attempt_at") WHERE (status = 'pending'::text);
