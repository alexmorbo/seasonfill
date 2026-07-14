-- modify "app_config" table
-- atlas:nolint destructive
ALTER TABLE "app_config" DROP COLUMN "auth_mode", DROP COLUMN "auth_local_bypass", DROP COLUMN "auth_local_networks";
-- AR-4: invalidate all existing session cookies (auth surface changed).
UPDATE "app_config" SET "auth_session_epoch" = "auth_session_epoch" + 1;
