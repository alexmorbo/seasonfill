DROP INDEX IF EXISTS episode_states_deleted_at;
ALTER TABLE episode_states DROP COLUMN deleted_at;
