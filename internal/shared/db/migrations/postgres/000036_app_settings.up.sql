-- Story 301: app-level settings singleton. Currently holds only the
-- operator-selected IANA timezone; future operator prefs (locale,
-- default theme) can land here without another migration.
--
-- Singleton enforced via id=1 CHECK. Same shape pattern as
-- runtime_config (id=1) but kept distinct so the watchdog-heavy
-- runtime_config row doesn't have to grow another column.
--
-- timezone NULL = "use TZ env var (or UTC if unset)". An explicit
-- empty string is normalised to NULL by the application layer so
-- the SQL invariant is: NULL or a non-empty IANA name validated by
-- time.LoadLocation in Go.

CREATE TABLE app_settings (
    id          smallint NOT NULL PRIMARY KEY DEFAULT 1,
    timezone    varchar(64),
    updated_at  timestamp with time zone NOT NULL,
    CONSTRAINT app_settings_singleton CHECK (id = 1)
);

INSERT INTO app_settings (id, timezone, updated_at)
VALUES (1, NULL, CURRENT_TIMESTAMP);
