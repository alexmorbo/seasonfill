-- See sqlite/000022 for backfill rationale (PLAN §6.1 F-R2-4).

-- add column "enrichment_text_synced_at" to table: "series"
ALTER TABLE "series" ADD COLUMN "enrichment_text_synced_at" timestamptz NULL;
-- add column "enrichment_cast_synced_at" to table: "series"
ALTER TABLE "series" ADD COLUMN "enrichment_cast_synced_at" timestamptz NULL;
-- add column "enrichment_recs_synced_at" to table: "series"
ALTER TABLE "series" ADD COLUMN "enrichment_recs_synced_at" timestamptz NULL;
-- add column "enrichment_media_synced_at" to table: "series"
ALTER TABLE "series" ADD COLUMN "enrichment_media_synced_at" timestamptz NULL;
-- add column "episodes_synced_at" to table: "seasons"
ALTER TABLE "seasons" ADD COLUMN "episodes_synced_at" timestamptz NULL;

-- create index "series_enrichment_text_synced_at_idx" to table: "series"
CREATE INDEX "series_enrichment_text_synced_at_idx" ON "series" ("enrichment_text_synced_at");
-- create index "series_enrichment_cast_synced_at_idx" to table: "series"
CREATE INDEX "series_enrichment_cast_synced_at_idx" ON "series" ("enrichment_cast_synced_at");
-- create index "series_enrichment_recs_synced_at_idx" to table: "series"
CREATE INDEX "series_enrichment_recs_synced_at_idx" ON "series" ("enrichment_recs_synced_at");
-- create index "series_enrichment_media_synced_at_idx" to table: "series"
CREATE INDEX "series_enrichment_media_synced_at_idx" ON "series" ("enrichment_media_synced_at");

-- backfill: copy canon-level Stage 1+2 stamp into 4 new section stamps.
UPDATE "series" SET
  "enrichment_text_synced_at"  = "enrichment_tmdb_synced_at",
  "enrichment_cast_synced_at"  = "enrichment_tmdb_synced_at",
  "enrichment_recs_synced_at"  = "enrichment_tmdb_synced_at",
  "enrichment_media_synced_at" = "enrichment_tmdb_synced_at"
WHERE "enrichment_text_synced_at" IS NULL
  AND "enrichment_tmdb_synced_at" IS NOT NULL;
