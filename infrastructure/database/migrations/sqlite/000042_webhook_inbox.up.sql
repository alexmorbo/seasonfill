-- create "webhook_inbox" table
CREATE TABLE `webhook_inbox` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `instance_name` text NOT NULL,
  `event_type` text NOT NULL,
  `payload` text NOT NULL,
  `status` text NOT NULL DEFAULT 'pending',
  `attempts` integer NOT NULL DEFAULT 0,
  `next_attempt_at` datetime NULL,
  `lease_until` datetime NULL,
  `last_error` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "webhook_inbox_pending" to table: "webhook_inbox"
CREATE INDEX `webhook_inbox_pending` ON `webhook_inbox` (`next_attempt_at`) WHERE status = 'pending';
