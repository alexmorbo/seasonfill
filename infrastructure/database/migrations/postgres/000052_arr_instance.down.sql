-- reverse Ф6-R-1: drop radarr settings, revert index/constraint names,
-- drop type column, rename table back to sonarr_instance.
DROP TABLE "radarr_instance_settings";
ALTER INDEX "arr_instance_unhealthy" RENAME TO "sonarr_instance_unhealthy";
ALTER TABLE "arr_instance" RENAME CONSTRAINT "arr_instance_token_secret_id_fkey" TO "sonarr_instance_token_secret_id_fkey";
ALTER TABLE "arr_instance" DROP COLUMN "type";
ALTER TABLE "arr_instance" RENAME TO "sonarr_instance";
