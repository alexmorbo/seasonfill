-- modify "user_instance_tags" table
ALTER TABLE "user_instance_tags" RENAME COLUMN "sonarr_tag_id" TO "arr_tag_id";
ALTER TABLE "user_instance_tags" RENAME COLUMN "sonarr_tag_label" TO "arr_tag_label";
