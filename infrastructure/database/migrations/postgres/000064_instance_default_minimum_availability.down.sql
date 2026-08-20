-- reverse: modify "sonarr_instance_settings" table
ALTER TABLE "sonarr_instance_settings" DROP COLUMN "default_minimum_availability";
-- reverse: modify "radarr_instance_settings" table
ALTER TABLE "radarr_instance_settings" DROP COLUMN "default_minimum_availability";
