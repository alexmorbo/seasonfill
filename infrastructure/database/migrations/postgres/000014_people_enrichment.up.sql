-- add "enrichment_synced_at" column to "people" table
ALTER TABLE "people" ADD COLUMN "enrichment_synced_at" timestamptz NULL;
