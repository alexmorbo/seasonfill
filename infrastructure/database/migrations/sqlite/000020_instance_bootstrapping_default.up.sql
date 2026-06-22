-- Story 488 (B-14) — fresh sonarr_instance INSERTs default to
-- 'Bootstrapping' so the initial registry seed and the row written by
-- Create() agree semantically. Existing rows are unchanged; only the
-- column DEFAULT changes. SQLite requires a table-rewrite for column
-- DEFAULT changes; this preserves data + recreates the unhealthy index
-- identically.
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_sonarr_instance" table
CREATE TABLE `new_sonarr_instance` (
  `name` text NOT NULL,
  `url` text NOT NULL,
  `public_url` text NULL,
  `mode` text NOT NULL DEFAULT 'auto',
  `token_secret_id` integer NULL,
  `health` text NOT NULL DEFAULT 'Bootstrapping',
  `last_check_at` datetime NULL,
  `transitions_count` integer NOT NULL DEFAULT 0,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`name`),
  CONSTRAINT `sonarr_instance_token_secret_id_fkey` FOREIGN KEY (`token_secret_id`) REFERENCES `instance_secret` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL
);
-- copy rows from old table "sonarr_instance" to new temporary table "new_sonarr_instance"
INSERT INTO `new_sonarr_instance` (`name`, `url`, `public_url`, `mode`, `token_secret_id`, `health`, `last_check_at`, `transitions_count`, `created_at`, `updated_at`) SELECT `name`, `url`, `public_url`, `mode`, `token_secret_id`, `health`, `last_check_at`, `transitions_count`, `created_at`, `updated_at` FROM `sonarr_instance`;
-- drop "sonarr_instance" table after copying rows
DROP TABLE `sonarr_instance`;
-- rename temporary table "new_sonarr_instance" to "sonarr_instance"
ALTER TABLE `new_sonarr_instance` RENAME TO `sonarr_instance`;
-- create index "sonarr_instance_unhealthy" to table: "sonarr_instance"
CREATE INDEX `sonarr_instance_unhealthy` ON `sonarr_instance` (`last_check_at`) WHERE health <> 'healthy';
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
