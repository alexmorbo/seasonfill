-- 046a: per-Decision season-stats snapshot. NOT NULL DEFAULT 0 mirrors
-- the existing missing_count / existing_count columns on this table —
-- pre-046a rows backfill cleanly and the new write path always supplies
-- a concrete int. Adding NOT NULL with a constant default is a metadata
-- update on Postgres ≥11, so no rewrite of the (potentially large) row
-- set is required.
ALTER TABLE decisions
    ADD COLUMN total_episodes integer NOT NULL DEFAULT 0,
    ADD COLUMN aired_episodes integer NOT NULL DEFAULT 0,
    ADD COLUMN existing_episodes integer NOT NULL DEFAULT 0,
    ADD COLUMN grabbed_episodes integer NOT NULL DEFAULT 0;
