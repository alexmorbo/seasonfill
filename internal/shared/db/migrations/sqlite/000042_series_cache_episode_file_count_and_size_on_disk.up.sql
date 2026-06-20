ALTER TABLE series_cache ADD COLUMN episode_file_count   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series_cache ADD COLUMN size_on_disk_bytes   BIGINT  NOT NULL DEFAULT 0;
