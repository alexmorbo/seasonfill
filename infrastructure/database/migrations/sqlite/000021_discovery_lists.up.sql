-- create "discovery_lists" table
CREATE TABLE `discovery_lists` (
  `kind` text NOT NULL,
  `param` text NOT NULL DEFAULT '',
  `language` text NOT NULL,
  `series_id` integer NOT NULL,
  `position` integer NOT NULL,
  `refreshed_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`kind`, `param`, `language`, `series_id`),
  CONSTRAINT `discovery_lists_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "discovery_lists_lookup_idx" to table: "discovery_lists"
CREATE INDEX `discovery_lists_lookup_idx` ON `discovery_lists` (`kind`, `param`, `language`, `position`);
-- create index "discovery_lists_refresh_idx" to table: "discovery_lists"
CREATE INDEX `discovery_lists_refresh_idx` ON `discovery_lists` (`kind`, `refreshed_at`);
