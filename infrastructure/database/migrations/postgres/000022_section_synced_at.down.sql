DROP INDEX "series_enrichment_media_synced_at_idx";
DROP INDEX "series_enrichment_recs_synced_at_idx";
DROP INDEX "series_enrichment_cast_synced_at_idx";
DROP INDEX "series_enrichment_text_synced_at_idx";
ALTER TABLE "seasons" DROP COLUMN "episodes_synced_at";
ALTER TABLE "series" DROP COLUMN "enrichment_media_synced_at";
ALTER TABLE "series" DROP COLUMN "enrichment_recs_synced_at";
ALTER TABLE "series" DROP COLUMN "enrichment_cast_synced_at";
ALTER TABLE "series" DROP COLUMN "enrichment_text_synced_at";
