-- 039f-1: SQLite mirror of 000007.
ALTER TABLE grab_records ADD COLUMN replay_of_id text;

-- SQLite partial-index support since 3.8 — glebarez driver ships well above.
CREATE INDEX idx_grab_records_replay_of_id
    ON grab_records (replay_of_id)
    WHERE replay_of_id IS NOT NULL;
