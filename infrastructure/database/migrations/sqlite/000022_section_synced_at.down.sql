-- reverse: create index "series_enrichment_media_synced_at_idx" to table: "series"
DROP INDEX `series_enrichment_media_synced_at_idx`;
-- reverse: create index "series_enrichment_recs_synced_at_idx" to table: "series"
DROP INDEX `series_enrichment_recs_synced_at_idx`;
-- reverse: create index "series_enrichment_cast_synced_at_idx" to table: "series"
DROP INDEX `series_enrichment_cast_synced_at_idx`;
-- reverse: create index "series_enrichment_text_synced_at_idx" to table: "series"
DROP INDEX `series_enrichment_text_synced_at_idx`;
-- reverse: add column "episodes_synced_at" to table: "seasons"
ALTER TABLE `seasons` DROP COLUMN `episodes_synced_at`;
-- reverse: add column "enrichment_media_synced_at" to table: "series"
ALTER TABLE `series` DROP COLUMN `enrichment_media_synced_at`;
-- reverse: add column "enrichment_recs_synced_at" to table: "series"
ALTER TABLE `series` DROP COLUMN `enrichment_recs_synced_at`;
-- reverse: add column "enrichment_cast_synced_at" to table: "series"
ALTER TABLE `series` DROP COLUMN `enrichment_cast_synced_at`;
-- reverse: add column "enrichment_text_synced_at" to table: "series"
ALTER TABLE `series` DROP COLUMN `enrichment_text_synced_at`;
