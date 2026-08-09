-- create "notification_outbox" table
CREATE TABLE `notification_outbox` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `event_type` text NOT NULL,
  `payload` text NOT NULL,
  `status` text NOT NULL DEFAULT 'pending',
  `attempts` integer NOT NULL DEFAULT 0,
  `next_attempt_at` datetime NULL,
  `dedup_key` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "notification_outbox_pending" to table: "notification_outbox"
CREATE INDEX `notification_outbox_pending` ON `notification_outbox` (`next_attempt_at`) WHERE status = 'pending';
-- create index "notification_outbox_dedup" to table: "notification_outbox"
CREATE INDEX `notification_outbox_dedup` ON `notification_outbox` (`dedup_key`) WHERE status = 'pending';
-- create "notification_agents" table
CREATE TABLE `notification_agents` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `name` text NOT NULL,
  `enabled` boolean NOT NULL DEFAULT false,
  `config_encrypted` bytea NOT NULL,
  `event_types` text NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
