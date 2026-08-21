-- rename column "sonarr_tag_id" to "arr_tag_id" on table: "user_instance_tags"
ALTER TABLE `user_instance_tags` RENAME COLUMN `sonarr_tag_id` TO `arr_tag_id`;
-- rename column "sonarr_tag_label" to "arr_tag_label" on table: "user_instance_tags"
ALTER TABLE `user_instance_tags` RENAME COLUMN `sonarr_tag_label` TO `arr_tag_label`;
