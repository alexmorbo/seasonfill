-- reverse: Story 488 (B-14) — restore previous DEFAULT 'unknown' on
-- sonarr_instance.health.
-- reverse: modify "sonarr_instance" table
ALTER TABLE "sonarr_instance" ALTER COLUMN "health" SET DEFAULT 'unknown';
