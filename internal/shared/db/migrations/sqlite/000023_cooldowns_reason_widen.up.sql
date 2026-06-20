-- 118: SQLite mirror of postgres 000023. The SQLite baseline declared
-- cooldowns.reason as `text` (storage class TEXT with no length
-- affinity), so widening is a no-op on this dialect. Kept so the
-- version index stays in lock-step with postgres.
SELECT 1;
