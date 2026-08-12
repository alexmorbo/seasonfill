-- reverse: rebuild all three back to their pre-058 (global) shapes.
PRAGMA foreign_keys = off;

-- restore notification_agents (drop user_id)
CREATE TABLE `new_notification_agents` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `name` text NOT NULL,
  `enabled` boolean NOT NULL DEFAULT false,
  `config_encrypted` bytea NOT NULL,
  `event_types` text NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
INSERT INTO `new_notification_agents` (`id`, `name`, `enabled`, `config_encrypted`, `event_types`, `created_at`)
  SELECT `id`, `name`, `enabled`, `config_encrypted`, `event_types`, `created_at` FROM `notification_agents`;
DROP TABLE `notification_agents`;
ALTER TABLE `new_notification_agents` RENAME TO `notification_agents`;

-- restore followed_series (single-column PK series_id)
CREATE TABLE `new_followed_series` (
  `series_id` integer NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`series_id`),
  CONSTRAINT `followed_series_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_followed_series` (`series_id`, `created_at`)
  SELECT `series_id`, `created_at` FROM `followed_series`;
DROP TABLE `followed_series`;
ALTER TABLE `new_followed_series` RENAME TO `followed_series`;

-- restore discovery_blocklist (global UNIQUE(kind, ref_id))
CREATE TABLE `new_discovery_blocklist` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `kind` text NOT NULL,
  `ref_id` bigint NOT NULL,
  `label` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
INSERT INTO `new_discovery_blocklist` (`id`, `kind`, `ref_id`, `label`, `created_at`)
  SELECT `id`, `kind`, `ref_id`, `label`, `created_at` FROM `discovery_blocklist`;
DROP TABLE `discovery_blocklist`;
ALTER TABLE `new_discovery_blocklist` RENAME TO `discovery_blocklist`;
CREATE UNIQUE INDEX `discovery_blocklist_kind_ref` ON `discovery_blocklist` (`kind`, `ref_id`);

PRAGMA foreign_keys = on;
