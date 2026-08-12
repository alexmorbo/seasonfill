-- create "requests" table
CREATE TABLE `requests` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `user_id` integer NOT NULL,
  `media_type` text NOT NULL,
  `tmdb_id` bigint NOT NULL,
  `seasons` text NULL,
  `payload` text NOT NULL,
  `status` text NOT NULL DEFAULT 'pending',
  `approver_id` integer NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `requests_approver_id_fkey` FOREIGN KEY (`approver_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT `requests_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "requests_status_idx" to table: "requests"
CREATE INDEX `requests_status_idx` ON `requests` (`status`);
-- create index "requests_user_id_idx" to table: "requests"
CREATE INDEX `requests_user_id_idx` ON `requests` (`user_id`);
-- create index "requests_pending_uniq" to table: "requests"
CREATE UNIQUE INDEX `requests_pending_uniq` ON `requests` (`user_id`, `media_type`, `tmdb_id`) WHERE status = 'pending';
-- Ф8-U-2 admin-perms backfill (sqlite boolean literal = 1)
UPDATE `users` SET `auto_approve` = 1, `request` = 1, `manage_requests` = 1, `manage_users` = 1, `request_4k` = 1 WHERE `role` = 'admin';
