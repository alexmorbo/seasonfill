-- create "discovery_blocklist" table
CREATE TABLE `discovery_blocklist` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `kind` text NOT NULL,
  `ref_id` bigint NOT NULL,
  `label` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "discovery_blocklist_kind_ref" to table: "discovery_blocklist"
CREATE UNIQUE INDEX `discovery_blocklist_kind_ref` ON `discovery_blocklist` (`kind`, `ref_id`);
