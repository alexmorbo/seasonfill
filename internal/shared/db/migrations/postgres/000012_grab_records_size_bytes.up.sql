-- 043b: persist Sonarr's release.size on grab_records (Phase 12, PRD B1).
-- BIGINT covers ≥ 9 exabytes — far above any realistic release.
-- Nullable on backfill — no historical sample is rewritten (PRD §5 #1).
ALTER TABLE grab_records ADD COLUMN size_bytes BIGINT NULL;
