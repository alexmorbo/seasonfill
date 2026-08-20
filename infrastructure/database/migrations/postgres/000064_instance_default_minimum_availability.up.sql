-- modify "radarr_instance_settings" table
ALTER TABLE "radarr_instance_settings" ADD COLUMN "default_minimum_availability" text NULL;
-- modify "sonarr_instance_settings" table
ALTER TABLE "sonarr_instance_settings" ADD COLUMN "default_minimum_availability" text NULL;
