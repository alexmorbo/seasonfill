-- 046a down (SQLite). DROP COLUMN supported since SQLite 3.35+; the
-- glebarez/go-sqlite driver ships ≥3.45 (verified in 041 migration).
ALTER TABLE decisions DROP COLUMN grabbed_episodes;
ALTER TABLE decisions DROP COLUMN existing_episodes;
ALTER TABLE decisions DROP COLUMN aired_episodes;
ALTER TABLE decisions DROP COLUMN total_episodes;
