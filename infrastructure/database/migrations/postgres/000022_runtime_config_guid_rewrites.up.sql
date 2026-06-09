-- Story 107: add an operator-curated tracker GUID substitution table to
-- the singleton runtime_config row. Stored as a JSON array of {from,to}
-- objects; default '[]' so every existing row migrates cleanly. NOT NULL
-- so reads never need a coalesce — the repo just unmarshals raw bytes.
-- TEXT (not jsonb) because the application is the schema authority for
-- this column and the per-entry caps are enforced application-side; we
-- get nothing from jsonb's structural queries here and TEXT keeps the
-- SQLite mirror byte-identical.
ALTER TABLE runtime_config
    ADD COLUMN guid_rewrites text NOT NULL DEFAULT '[]';
