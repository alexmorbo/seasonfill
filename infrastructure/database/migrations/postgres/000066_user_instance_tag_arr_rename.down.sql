-- reverse: modify "user_instance_tags" table
ALTER TABLE "user_instance_tags" RENAME COLUMN "arr_tag_label" TO "sonarr_tag_label";
ALTER TABLE "user_instance_tags" RENAME COLUMN "arr_tag_id" TO "sonarr_tag_id";
