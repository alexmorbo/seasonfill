-- reverse: create "user_instance_access" table
DROP TABLE "user_instance_access";
-- reverse: modify "users" table
ALTER TABLE "users" DROP COLUMN "request_4k", DROP COLUMN "manage_users", DROP COLUMN "manage_requests", DROP COLUMN "request", DROP COLUMN "auto_approve";
