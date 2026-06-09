-- 091a / F-P2-2: capture grab intent on every Decision row. The
-- application layer writes a JSON document with four fields:
-- target_episodes, had_episodes, chosen_because, chosen_reason_detail.
-- Nullable on purpose — pre-091a rows have no intent and the frontend
-- handles `null` as "we don't know why". 4 KiB cap is enforced at
-- write-time by application/errtext.Clamp; the column type is plain
-- jsonb so future evolution (adding axes) costs no migration.
ALTER TABLE decisions
    ADD COLUMN intent jsonb;
