-- reverse: add column "enrichment_keywords_synced_at" to table: "movies"
ALTER TABLE `movies` DROP COLUMN `enrichment_keywords_synced_at`;
-- reverse: add column "enrichment_media_synced_at" to table: "movies"
ALTER TABLE `movies` DROP COLUMN `enrichment_media_synced_at`;
-- reverse: add column "enrichment_recs_synced_at" to table: "movies"
ALTER TABLE `movies` DROP COLUMN `enrichment_recs_synced_at`;
-- reverse: add column "enrichment_cast_synced_at" to table: "movies"
ALTER TABLE `movies` DROP COLUMN `enrichment_cast_synced_at`;
-- reverse: add column "enrichment_text_synced_at" to table: "movies"
ALTER TABLE `movies` DROP COLUMN `enrichment_text_synced_at`;
