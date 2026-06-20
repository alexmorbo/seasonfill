-- 039f-1 down (SQLite). DROP COLUMN supported since SQLite 3.35+; the
-- glebarez/go-sqlite driver ships ≥3.45.
DROP INDEX IF EXISTS idx_grab_records_replay_of_id;
ALTER TABLE grab_records DROP COLUMN replay_of_id;
