-- 091a / F-P2-2: SQLite mirror of postgres 000021. SQLite has no native
-- JSON type — TEXT storage class with no length affinity carries the
-- JSON document as serialised bytes. The GORM model declares it as
-- `type:text` so the round-trip stays byte-identical.
ALTER TABLE decisions
    ADD COLUMN intent text;
