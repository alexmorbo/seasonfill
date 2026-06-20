-- 082: SQLite mirror of postgres 000019. Same nullable text column.
ALTER TABLE instance_qbit_settings
    ADD COLUMN qbit_public_url text;
