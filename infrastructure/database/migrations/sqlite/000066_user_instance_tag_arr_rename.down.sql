-- reverse: rename column "sonarr_tag_label" to "arr_tag_label" on table: "user_instance_tags"
ALTER TABLE `user_instance_tags` RENAME COLUMN `arr_tag_label` TO `sonarr_tag_label`;
-- reverse: rename column "sonarr_tag_id" to "arr_tag_id" on table: "user_instance_tags"
ALTER TABLE `user_instance_tags` RENAME COLUMN `arr_tag_id` TO `sonarr_tag_id`;
