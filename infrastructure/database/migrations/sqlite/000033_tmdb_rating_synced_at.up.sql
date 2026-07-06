-- W18-11 (F-01 fix): dedicated on-view TMDB rating freshness clock. Splits the
-- rating-only /ratings refresher off the SHARED enrichment_tmdb_synced_at column
-- (the full-enrichment TTL gate) so a once-per-TTL rating view no longer resets
-- the full series-worker's re-sync clock (missed status flips / seasons / cast).
-- Backfill: seed from enrichment_tmdb_synced_at for rows that already have a
-- rating. Rating-less rows stay NULL → the first on-view refresh fetches.

-- add column "tmdb_rating_synced_at" to table: "series"
ALTER TABLE `series` ADD COLUMN `tmdb_rating_synced_at` datetime NULL;

-- backfill: copy the full-enrichment stamp into the new rating clock for rows
-- that already carry a rating.
UPDATE `series` SET `tmdb_rating_synced_at` = `enrichment_tmdb_synced_at`
WHERE `tmdb_rating` IS NOT NULL;
