-- Ф8-U-5: per-user retrofit — SQLite rebuild envelope (see postgres sibling
-- + 000052 for the pattern). user_id backfilled to the seed admin.
PRAGMA foreign_keys = off;

-- rebuild discovery_blocklist -> per-user, UNIQUE(user_id, kind, ref_id)
-- atlas:nolint MF103
CREATE TABLE `new_discovery_blocklist` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `user_id` integer NOT NULL,
  `kind` text NOT NULL,
  `ref_id` bigint NOT NULL,
  `label` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `discovery_blocklist_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_discovery_blocklist` (`id`, `user_id`, `kind`, `ref_id`, `label`, `created_at`)
  SELECT `id`, (SELECT `id` FROM `users` WHERE `role` = 'admin' ORDER BY `id` ASC LIMIT 1),
         `kind`, `ref_id`, `label`, `created_at` FROM `discovery_blocklist`;
-- atlas:nolint
DROP TABLE `discovery_blocklist`;
ALTER TABLE `new_discovery_blocklist` RENAME TO `discovery_blocklist`;
-- atlas:nolint
CREATE UNIQUE INDEX `discovery_blocklist_kind_ref` ON `discovery_blocklist` (`user_id`, `kind`, `ref_id`);

-- rebuild followed_series -> composite PK (user_id, series_id)
-- atlas:nolint MF103
CREATE TABLE `new_followed_series` (
  `user_id` integer NOT NULL,
  `series_id` integer NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`user_id`, `series_id`),
  CONSTRAINT `followed_series_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `followed_series_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_followed_series` (`user_id`, `series_id`, `created_at`)
  SELECT (SELECT `id` FROM `users` WHERE `role` = 'admin' ORDER BY `id` ASC LIMIT 1),
         `series_id`, `created_at` FROM `followed_series`;
-- atlas:nolint
DROP TABLE `followed_series`;
ALTER TABLE `new_followed_series` RENAME TO `followed_series`;

-- rebuild notification_agents -> +user_id (surrogate PK id stays)
-- atlas:nolint MF103
CREATE TABLE `new_notification_agents` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `user_id` integer NOT NULL,
  `name` text NOT NULL,
  `enabled` boolean NOT NULL DEFAULT false,
  `config_encrypted` bytea NOT NULL,
  `event_types` text NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `notification_agents_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_notification_agents` (`id`, `user_id`, `name`, `enabled`, `config_encrypted`, `event_types`, `created_at`)
  SELECT `id`, (SELECT `id` FROM `users` WHERE `role` = 'admin' ORDER BY `id` ASC LIMIT 1),
         `name`, `enabled`, `config_encrypted`, `event_types`, `created_at` FROM `notification_agents`;
-- atlas:nolint
DROP TABLE `notification_agents`;
ALTER TABLE `new_notification_agents` RENAME TO `notification_agents`;

PRAGMA foreign_keys = on;
