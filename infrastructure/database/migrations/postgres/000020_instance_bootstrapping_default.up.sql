-- Story 488 (B-14) — fresh sonarr_instance INSERTs default to
-- 'Bootstrapping' so the initial registry seed and the row written by
-- Create() agree semantically. Existing rows are unchanged; only the
-- column DEFAULT changes.
-- modify "sonarr_instance" table
ALTER TABLE "sonarr_instance" ALTER COLUMN "health" SET DEFAULT 'Bootstrapping';
