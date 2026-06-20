-- 091a / F-P2-2: SQLite mirror of postgres 000021.down. SQLite supports
-- DROP COLUMN since 3.35; the test runner pins a modern build so the
-- statement compiles cleanly.
ALTER TABLE decisions
    DROP COLUMN intent;
