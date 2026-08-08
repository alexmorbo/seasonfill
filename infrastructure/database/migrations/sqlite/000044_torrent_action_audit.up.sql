-- create "torrent_action_audit" table
CREATE TABLE `torrent_action_audit` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `instance_name` text NOT NULL,
  `hash` text NOT NULL,
  `action` text NOT NULL,
  `actor` text NOT NULL,
  `result` text NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "torrent_action_audit_hash_created_idx" to table: "torrent_action_audit"
CREATE INDEX `torrent_action_audit_hash_created_idx` ON `torrent_action_audit` (`hash`, `created_at`);
