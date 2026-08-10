-- create "discovery_blocklist" table
CREATE TABLE "discovery_blocklist" (
  "id" bigserial NOT NULL,
  "kind" text NOT NULL,
  "ref_id" bigint NOT NULL,
  "label" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- create index "discovery_blocklist_kind_ref" to table: "discovery_blocklist"
CREATE UNIQUE INDEX "discovery_blocklist_kind_ref" ON "discovery_blocklist" ("kind", "ref_id");
