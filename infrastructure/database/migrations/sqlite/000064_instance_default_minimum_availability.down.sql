-- reverse: add column "default_minimum_availability" to table: "radarr_instance_settings"
ALTER TABLE `radarr_instance_settings` DROP COLUMN `default_minimum_availability`;
-- reverse: add column "default_minimum_availability" to table: "sonarr_instance_settings"
ALTER TABLE `sonarr_instance_settings` DROP COLUMN `default_minimum_availability`;
