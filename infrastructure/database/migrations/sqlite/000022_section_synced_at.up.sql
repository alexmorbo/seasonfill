-- Add per-section enrichment sync timestamps + per-season episodes sync stamp.
-- A1 ships columns + backfill; A2-A5 ship the writers that bump these on UPSERT.
-- Backfill rationale: PLAN §6.1 F-R2-4 (Round 2 closure).
-- enrichment_tmdb_synced_at is the existing canon-level Stage 1+2 stamp —
-- semantically accurate proxy for "we have series_texts/cast/recs/media for
-- this row". updated_at would over-suppress because Story 549 Layered Freshener
-- bumps it on every read-through.

-- add column "enrichment_text_synced_at" to table: "series"
ALTER TABLE `series` ADD COLUMN `enrichment_text_synced_at` datetime NULL;
-- add column "enrichment_cast_synced_at" to table: "series"
ALTER TABLE `series` ADD COLUMN `enrichment_cast_synced_at` datetime NULL;
-- add column "enrichment_recs_synced_at" to table: "series"
ALTER TABLE `series` ADD COLUMN `enrichment_recs_synced_at` datetime NULL;
-- add column "enrichment_media_synced_at" to table: "series"
ALTER TABLE `series` ADD COLUMN `enrichment_media_synced_at` datetime NULL;
-- add column "episodes_synced_at" to table: "seasons"
ALTER TABLE `seasons` ADD COLUMN `episodes_synced_at` datetime NULL;

-- create index "series_enrichment_text_synced_at_idx" to table: "series"
CREATE INDEX `series_enrichment_text_synced_at_idx` ON `series` (`enrichment_text_synced_at`);
-- create index "series_enrichment_cast_synced_at_idx" to table: "series"
CREATE INDEX `series_enrichment_cast_synced_at_idx` ON `series` (`enrichment_cast_synced_at`);
-- create index "series_enrichment_recs_synced_at_idx" to table: "series"
CREATE INDEX `series_enrichment_recs_synced_at_idx` ON `series` (`enrichment_recs_synced_at`);
-- create index "series_enrichment_media_synced_at_idx" to table: "series"
CREATE INDEX `series_enrichment_media_synced_at_idx` ON `series` (`enrichment_media_synced_at`);

-- backfill: copy canon-level Stage 1+2 stamp into 4 new section stamps.
-- Stub rows (enrichment_tmdb_synced_at NULL) keep NULL → Probe triggers refresh.
UPDATE `series` SET
  `enrichment_text_synced_at`  = `enrichment_tmdb_synced_at`,
  `enrichment_cast_synced_at`  = `enrichment_tmdb_synced_at`,
  `enrichment_recs_synced_at`  = `enrichment_tmdb_synced_at`,
  `enrichment_media_synced_at` = `enrichment_tmdb_synced_at`
WHERE `enrichment_text_synced_at` IS NULL
  AND `enrichment_tmdb_synced_at` IS NOT NULL;
