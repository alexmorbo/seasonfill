-- Ф8-U-5c: SQLite rebuild envelope (see postgres sibling + 000058 for the
-- pattern). user_id backfilled to the seed admin.
PRAGMA foreign_keys = off;

-- rebuild notification_outbox -> +user_id (surrogate PK id stays)
-- atlas:nolint MF103
CREATE TABLE `new_notification_outbox` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `user_id` integer NOT NULL,
  `event_type` text NOT NULL,
  `payload` text NOT NULL,
  `status` text NOT NULL DEFAULT 'pending',
  `attempts` integer NOT NULL DEFAULT 0,
  `next_attempt_at` datetime NULL,
  `dedup_key` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `notification_outbox_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_notification_outbox` (`id`, `user_id`, `event_type`, `payload`, `status`, `attempts`, `next_attempt_at`, `dedup_key`, `created_at`)
  SELECT `id`, (SELECT `id` FROM `users` WHERE `role` = 'admin' ORDER BY `id` ASC LIMIT 1),
         `event_type`, `payload`, `status`, `attempts`, `next_attempt_at`, `dedup_key`, `created_at`
  FROM `notification_outbox`;
-- atlas:nolint
DROP TABLE `notification_outbox`;
ALTER TABLE `new_notification_outbox` RENAME TO `notification_outbox`;
-- atlas:nolint
CREATE INDEX `notification_outbox_pending` ON `notification_outbox` (`next_attempt_at`) WHERE status = 'pending';
-- atlas:nolint
CREATE INDEX `notification_outbox_dedup` ON `notification_outbox` (`dedup_key`) WHERE status = 'pending';
-- atlas:nolint
CREATE INDEX `notification_outbox_user_pending` ON `notification_outbox` (`user_id`, `status`, `next_attempt_at`);

-- rebuild notified_events -> per-user PK (user_id, event_type, entity_key)
-- atlas:nolint MF103
CREATE TABLE `new_notified_events` (
  `user_id` integer NOT NULL,
  `event_type` text NOT NULL,
  `entity_key` text NOT NULL,
  `first_seen_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`user_id`, `event_type`, `entity_key`),
  CONSTRAINT `notified_events_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
INSERT INTO `new_notified_events` (`user_id`, `event_type`, `entity_key`, `first_seen_at`)
  SELECT (SELECT `id` FROM `users` WHERE `role` = 'admin' ORDER BY `id` ASC LIMIT 1),
         `event_type`, `entity_key`, `first_seen_at` FROM `notified_events`;
-- atlas:nolint
DROP TABLE `notified_events`;
ALTER TABLE `new_notified_events` RENAME TO `notified_events`;

PRAGMA foreign_keys = on;
