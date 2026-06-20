-- SQLite ALTER TABLE ADD COLUMN cannot carry a CHECK constraint that
-- references the new column without rebuilding the table; the
-- application layer (runtimeconfig.UseCase) is the single enforcement
-- point for the mode-enum invariant. Defaults match the postgres
-- migration verbatim so a SQLite test DB matches prod row-shape.
ALTER TABLE runtime_config ADD COLUMN auth_mode text NOT NULL DEFAULT 'forms';
ALTER TABLE runtime_config ADD COLUMN auth_local_bypass numeric NOT NULL DEFAULT 0;
ALTER TABLE runtime_config ADD COLUMN auth_local_networks text NOT NULL DEFAULT
    '["127.0.0.0/8","::1/128","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","169.254.0.0/16","fe80::/10","fc00::/7"]';
ALTER TABLE runtime_config ADD COLUMN auth_session_epoch integer NOT NULL DEFAULT 0;
