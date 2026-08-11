-- add column "auto_approve" to table: "users"
ALTER TABLE `users` ADD COLUMN `auto_approve` boolean NOT NULL DEFAULT false;
-- add column "request" to table: "users"
ALTER TABLE `users` ADD COLUMN `request` boolean NOT NULL DEFAULT false;
-- add column "manage_requests" to table: "users"
ALTER TABLE `users` ADD COLUMN `manage_requests` boolean NOT NULL DEFAULT false;
-- add column "manage_users" to table: "users"
ALTER TABLE `users` ADD COLUMN `manage_users` boolean NOT NULL DEFAULT false;
-- add column "request_4k" to table: "users"
ALTER TABLE `users` ADD COLUMN `request_4k` boolean NOT NULL DEFAULT false;
-- create "user_instance_access" table
CREATE TABLE `user_instance_access` (
  `user_id` integer NOT NULL,
  `instance_name` text NOT NULL,
  `can_request` boolean NOT NULL DEFAULT true,
  PRIMARY KEY (`user_id`, `instance_name`),
  CONSTRAINT `user_instance_access_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
