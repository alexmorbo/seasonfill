-- reverse: add default_quality_profile_id / default_root_folder_path to sonarr_instance_settings
ALTER TABLE "sonarr_instance_settings" DROP COLUMN "default_root_folder_path";
ALTER TABLE "sonarr_instance_settings" DROP COLUMN "default_quality_profile_id";
