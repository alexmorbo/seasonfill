-- W18-16: dedicated on-view SKELETON freshness clock. The on-view worker
-- HandleForcedLang deliberately never stamps enrichment_tmdb_synced_at (it would
-- short-circuit the follow-up full Handle), so the skeleton TTL gate that read
-- that shared column was PERMANENTLY stale — every /series/:id view re-fired a
-- ~1.5s TMDB GetTV and re-committed the canon upsert (churning series.updated_at
-- = client synced_at). This column is stamped ONLY by MarkSkeletonSynced on a
-- real on-view skeleton commit, giving the progressive-TTL SWR gate a clock that
-- actually advances.
--
-- Backfill: seed from enrichment_tmdb_synced_at — the last full enrichment is the
-- last time the skeleton canon was known-fresh, so no library row is falsely cold
-- on deploy (avoids a one-time library-wide blocking-fetch stampede). Rows never
-- TMDB-enriched stay NULL → treated as cold → first on-view blocks + fetches.

ALTER TABLE "series" ADD COLUMN "skeleton_synced_at" timestamptz NULL;

UPDATE "series" SET "skeleton_synced_at" = "enrichment_tmdb_synced_at"
WHERE "enrichment_tmdb_synced_at" IS NOT NULL;
