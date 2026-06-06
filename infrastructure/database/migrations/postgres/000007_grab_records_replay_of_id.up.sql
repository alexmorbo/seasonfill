-- 039f-1: audit pointer from re-grabbed row to original grab_records row.
-- Watchdog re-grabs (Phase 10) populate this; manual scan / rescan paths
-- leave it NULL. The column is nullable on purpose — only re-grab rows
-- carry a non-NULL value.
ALTER TABLE grab_records
    ADD COLUMN replay_of_id text;

CREATE INDEX idx_grab_records_replay_of_id
    ON grab_records (replay_of_id)
    WHERE replay_of_id IS NOT NULL;
