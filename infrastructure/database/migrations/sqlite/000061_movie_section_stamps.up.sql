-- add column "enrichment_text_synced_at" to table: "movies"
ALTER TABLE `movies` ADD COLUMN `enrichment_text_synced_at` datetime NULL;
-- add column "enrichment_cast_synced_at" to table: "movies"
ALTER TABLE `movies` ADD COLUMN `enrichment_cast_synced_at` datetime NULL;
-- add column "enrichment_recs_synced_at" to table: "movies"
ALTER TABLE `movies` ADD COLUMN `enrichment_recs_synced_at` datetime NULL;
-- add column "enrichment_media_synced_at" to table: "movies"
ALTER TABLE `movies` ADD COLUMN `enrichment_media_synced_at` datetime NULL;
-- add column "enrichment_keywords_synced_at" to table: "movies"
ALTER TABLE `movies` ADD COLUMN `enrichment_keywords_synced_at` datetime NULL;
