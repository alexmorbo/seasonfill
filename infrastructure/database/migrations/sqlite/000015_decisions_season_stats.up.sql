-- 046a: per-Decision season-stats snapshot. Default 0 (not NULL) so
-- pre-046a rows look like "unknown" rather than null on read — the UI
-- consumes these as plain ints and degrades to a "—" placeholder when
-- the value is 0. NOT NULL matches the convention already used by
-- missing_count / existing_count on the same table.
ALTER TABLE decisions ADD COLUMN total_episodes integer NOT NULL DEFAULT 0;
ALTER TABLE decisions ADD COLUMN aired_episodes integer NOT NULL DEFAULT 0;
ALTER TABLE decisions ADD COLUMN existing_episodes integer NOT NULL DEFAULT 0;
ALTER TABLE decisions ADD COLUMN grabbed_episodes integer NOT NULL DEFAULT 0;
