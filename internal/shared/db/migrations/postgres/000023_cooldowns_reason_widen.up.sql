-- 118: widen cooldowns.reason from varchar(128) to text so a Sonarr
-- error string passed through activateGUIDCooldown (grab_usecase.go)
-- can be persisted without truncation. Parallels migration 20 which
-- did the same for decisions.error_detail (story 092). The
-- application-side cap stays at cooldown.ReasonMaxBytes (512 bytes);
-- this column is operator-facing, not a stack-trace sink.
ALTER TABLE cooldowns
    ALTER COLUMN reason TYPE text;
