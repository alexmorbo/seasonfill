-- reverse: create "user_instance_access" table
DROP TABLE `user_instance_access`;
-- reverse: add column "request_4k" to table: "users"
ALTER TABLE `users` DROP COLUMN `request_4k`;
-- reverse: add column "manage_users" to table: "users"
ALTER TABLE `users` DROP COLUMN `manage_users`;
-- reverse: add column "manage_requests" to table: "users"
ALTER TABLE `users` DROP COLUMN `manage_requests`;
-- reverse: add column "request" to table: "users"
ALTER TABLE `users` DROP COLUMN `request`;
-- reverse: add column "auto_approve" to table: "users"
ALTER TABLE `users` DROP COLUMN `auto_approve`;
