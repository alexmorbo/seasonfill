-- create "discovery_rows" table
CREATE TABLE "discovery_rows" (
  "id" bigserial NOT NULL,
  "row_type" text NOT NULL,
  "source" text NOT NULL,
  "media_type" text NOT NULL DEFAULT 'tv',
  "params" jsonb NOT NULL,
  "position" integer NOT NULL DEFAULT 0,
  "enabled" boolean NOT NULL DEFAULT true,
  "title" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- create index "discovery_rows_position_idx" to table: "discovery_rows"
CREATE INDEX "discovery_rows_position_idx" ON "discovery_rows" ("position");
