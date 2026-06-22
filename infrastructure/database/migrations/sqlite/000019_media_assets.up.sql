-- create "media_assets" table
CREATE TABLE `media_assets` (
  `hash` text NOT NULL,
  `source_url` text NOT NULL,
  `kind` text NOT NULL,
  `status` text NOT NULL DEFAULT 'pending',
  `content_type` text NULL,
  `size_bytes` bigint NULL,
  `fetched_at` datetime NULL,
  `last_access_at` datetime NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`hash`)
);
-- create index "idx_media_assets_source_url" to table: "media_assets"
CREATE UNIQUE INDEX `idx_media_assets_source_url` ON `media_assets` (`source_url`);
-- create index "idx_media_assets_status" to table: "media_assets"
CREATE INDEX `idx_media_assets_status` ON `media_assets` (`status`);
