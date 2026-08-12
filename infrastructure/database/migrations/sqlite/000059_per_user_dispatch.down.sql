-- reverse: rebuild both back to their pre-059 (global) shapes.
PRAGMA foreign_keys = off;

-- restore notified_events ((event_type, entity_key) PK, no user_id)
CREATE TABLE `new_notified_events` (
  `event_type` text NOT NULL,
  `entity_key` text NOT NULL,
  `first_seen_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`event_type`, `entity_key`)
);
INSERT INTO `new_notified_events` (`event_type`, `entity_key`, `first_seen_at`)
  SELECT `event_type`, `entity_key`, `first_seen_at` FROM `notified_events`;
DROP TABLE `notified_events`;
ALTER TABLE `new_notified_events` RENAME TO `notified_events`;

-- restore notification_outbox (no user_id)
CREATE TABLE `new_notification_outbox` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `event_type` text NOT NULL,
  `payload` text NOT NULL,
  `status` text NOT NULL DEFAULT 'pending',
  `attempts` integer NOT NULL DEFAULT 0,
  `next_attempt_at` datetime NULL,
  `dedup_key` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
INSERT INTO `new_notification_outbox` (`id`, `event_type`, `payload`, `status`, `attempts`, `next_attempt_at`, `dedup_key`, `created_at`)
  SELECT `id`, `event_type`, `payload`, `status`, `attempts`, `next_attempt_at`, `dedup_key`, `created_at`
  FROM `notification_outbox`;
DROP TABLE `notification_outbox`;
ALTER TABLE `new_notification_outbox` RENAME TO `notification_outbox`;
CREATE INDEX `notification_outbox_pending` ON `notification_outbox` (`next_attempt_at`) WHERE status = 'pending';
CREATE INDEX `notification_outbox_dedup` ON `notification_outbox` (`dedup_key`) WHERE status = 'pending';

PRAGMA foreign_keys = on;
