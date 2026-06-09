-- Story 107: SQLite mirror of postgres 000022. Same default '[]' so a
-- legacy SQLite test DB migrates to the same row shape as prod.
ALTER TABLE runtime_config ADD COLUMN guid_rewrites text NOT NULL DEFAULT '[]';
