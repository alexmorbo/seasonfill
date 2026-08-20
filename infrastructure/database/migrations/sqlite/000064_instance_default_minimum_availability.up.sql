-- add column "default_minimum_availability" to table: "sonarr_instance_settings"
ALTER TABLE `sonarr_instance_settings` ADD COLUMN `default_minimum_availability` text NULL;
-- add column "default_minimum_availability" to table: "radarr_instance_settings"
ALTER TABLE `radarr_instance_settings` ADD COLUMN `default_minimum_availability` text NULL;
