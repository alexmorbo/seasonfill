-- create "users" table
CREATE TABLE `users` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `username` text NOT NULL,
  `email` text NULL,
  `password_hash` text NULL,
  `oidc_subject` text NULL,
  `role` text NOT NULL DEFAULT 'admin',
  `avatar_mode` text NOT NULL DEFAULT 'auto',
  `preferred_language` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `last_login_at` datetime NULL,
  CONSTRAINT `users_role_check` CHECK (role IN ('admin', 'user')),
  CONSTRAINT `users_avatar_mode_check` CHECK (avatar_mode IN ('auto', 'monogram', 'gravatar'))
);
-- create index "users_username_uniq" to table: "users"
CREATE UNIQUE INDEX `users_username_uniq` ON `users` (`username`);
-- create index "users_oidc_subject_uniq" to table: "users"
CREATE UNIQUE INDEX `users_oidc_subject_uniq` ON `users` (`oidc_subject`) WHERE oidc_subject IS NOT NULL;
-- create "user_instance_tags" table
CREATE TABLE `user_instance_tags` (
  `user_id` integer NOT NULL,
  `instance_name` text NOT NULL,
  `sonarr_tag_id` integer NOT NULL,
  `sonarr_tag_label` text NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`user_id`, `instance_name`),
  CONSTRAINT `user_instance_tags_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `user_instance_tags_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "user_instance_tags_label" to table: "user_instance_tags"
CREATE UNIQUE INDEX `user_instance_tags_label` ON `user_instance_tags` (`instance_name`, `sonarr_tag_label`);
