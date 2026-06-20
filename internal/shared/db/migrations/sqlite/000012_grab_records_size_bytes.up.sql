-- 043b: persist Sonarr's release.size on grab_records (Phase 12, PRD B1).
-- Nullable on purpose — pre-Phase-12 rows have no recorded size and stay
-- NULL forever (PRD §5 #1, no backfill).
ALTER TABLE grab_records ADD COLUMN size_bytes INTEGER NULL;
