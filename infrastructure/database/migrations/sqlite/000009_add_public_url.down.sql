-- 041 down (SQLite). DROP COLUMN supported since SQLite 3.35+; the
-- glebarez/go-sqlite driver ships ≥3.45.
ALTER TABLE sonarr_instance DROP COLUMN public_url;
