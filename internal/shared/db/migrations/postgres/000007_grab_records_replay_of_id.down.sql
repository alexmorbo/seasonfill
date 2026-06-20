-- 039f-1 down: reverse the audit-pointer column.
DROP INDEX IF EXISTS idx_grab_records_replay_of_id;
ALTER TABLE grab_records DROP COLUMN IF EXISTS replay_of_id;
