-- add column "media_direct" to table: "app_config"
ALTER TABLE `app_config` ADD COLUMN `media_direct` boolean NOT NULL DEFAULT false;
