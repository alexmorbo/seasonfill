-- Story 301: sqlite mirror of 000036. Type mapping:
--   * smallint            → integer
--   * varchar(64)         → text
--   * timestamptz         → datetime
-- The CHECK constraint is preserved verbatim (sqlite supports it).

CREATE TABLE app_settings (
    id          integer NOT NULL PRIMARY KEY DEFAULT 1,
    timezone    text,
    updated_at  datetime NOT NULL,
    CONSTRAINT app_settings_singleton CHECK (id = 1)
);

INSERT INTO app_settings (id, timezone, updated_at)
VALUES (1, NULL, CURRENT_TIMESTAMP);
