-- reverse: modify "app_config" table
ALTER TABLE `app_config` ADD COLUMN `auth_mode` text NOT NULL DEFAULT 'forms';
ALTER TABLE `app_config` ADD COLUMN `auth_local_bypass` boolean NOT NULL DEFAULT false;
ALTER TABLE `app_config` ADD COLUMN `auth_local_networks` text NOT NULL DEFAULT '[]';
