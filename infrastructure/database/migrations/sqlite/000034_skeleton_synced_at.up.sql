-- W18-16: dedicated on-view SKELETON freshness clock (see postgres up for the
-- full rationale). Split off the shared enrichment_tmdb_synced_at so a rating/
-- skeleton on-view refresh cannot re-time — or be starved by — the full worker.
-- Backfill from enrichment_tmdb_synced_at; never-enriched rows stay NULL (cold).

ALTER TABLE `series` ADD COLUMN `skeleton_synced_at` datetime NULL;

UPDATE `series` SET `skeleton_synced_at` = `enrichment_tmdb_synced_at`
WHERE `enrichment_tmdb_synced_at` IS NOT NULL;
