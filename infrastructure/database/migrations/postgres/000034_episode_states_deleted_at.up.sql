-- 218 (E-2): episode_states soft-delete column for the SeriesDelete
-- cascade. Mirrors the series_cache.deleted_at pattern. Production
-- readers gain `AND deleted_at IS NULL`; the Upsert path is unchanged
-- (deleted_at stays NULL on fresh rows).
ALTER TABLE episode_states ADD COLUMN deleted_at timestamp with time zone;
CREATE INDEX episode_states_deleted_at
    ON episode_states (instance_name, deleted_at)
    WHERE deleted_at IS NOT NULL;
