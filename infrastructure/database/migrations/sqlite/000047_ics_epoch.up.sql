-- add column "ics_epoch" to table: "app_config"
ALTER TABLE `app_config` ADD COLUMN `ics_epoch` bigint NOT NULL DEFAULT 0;
