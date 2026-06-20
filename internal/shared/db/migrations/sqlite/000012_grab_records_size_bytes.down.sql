-- 043b down (SQLite). DROP COLUMN supported since SQLite 3.35+; the
-- glebarez/go-sqlite driver ships ≥3.45.
ALTER TABLE grab_records DROP COLUMN size_bytes;
