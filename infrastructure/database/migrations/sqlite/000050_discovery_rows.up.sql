-- create "discovery_rows" table
CREATE TABLE `discovery_rows` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `row_type` text NOT NULL,
  `source` text NOT NULL,
  `media_type` text NOT NULL DEFAULT 'tv',
  `params` text NOT NULL,
  `position` integer NOT NULL DEFAULT 0,
  `enabled` boolean NOT NULL DEFAULT true,
  `title` text NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "discovery_rows_position_idx" to table: "discovery_rows"
CREATE INDEX `discovery_rows_position_idx` ON `discovery_rows` (`position`);
