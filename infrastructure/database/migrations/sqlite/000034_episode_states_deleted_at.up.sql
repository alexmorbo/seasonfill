-- 218 (E-2): sqlite mirror. Partial-index predicate dropped for
-- portability — full index is fine at this scale.
ALTER TABLE episode_states ADD COLUMN deleted_at datetime;
CREATE INDEX episode_states_deleted_at
    ON episode_states (instance_name, deleted_at);
