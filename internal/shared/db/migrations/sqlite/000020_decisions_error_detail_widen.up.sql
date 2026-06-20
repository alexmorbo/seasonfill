-- 092 / F-P2-4: SQLite mirror of postgres 000020. The SQLite baseline
-- declared decisions.error_detail as `text` (storage class TEXT with
-- no length affinity), so the widening is a no-op on this dialect.
-- This migration exists only so the version index stays in sync with
-- postgres; SQLite simply executes a no-op statement.
SELECT 1;
