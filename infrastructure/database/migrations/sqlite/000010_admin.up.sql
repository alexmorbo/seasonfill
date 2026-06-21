-- create "sonarr_instance" table
CREATE TABLE `sonarr_instance` (
  `name` text NOT NULL,
  `url` text NOT NULL,
  `public_url` text NULL,
  `mode` text NOT NULL DEFAULT 'auto',
  `token_secret_id` integer NULL,
  `health` text NOT NULL DEFAULT 'unknown',
  `last_check_at` datetime NULL,
  `transitions_count` integer NOT NULL DEFAULT 0,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`name`),
  CONSTRAINT `sonarr_instance_token_secret_id_fkey` FOREIGN KEY (`token_secret_id`) REFERENCES `instance_secret` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL
);
-- create index "sonarr_instance_unhealthy" to table: "sonarr_instance"
CREATE INDEX `sonarr_instance_unhealthy` ON `sonarr_instance` (`last_check_at`) WHERE health <> 'healthy';
-- create "instance_secret" table
CREATE TABLE `instance_secret` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `instance_name` text NOT NULL,
  `secret_name` text NOT NULL,
  `encrypted_value` bytea NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  CONSTRAINT `instance_secret_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "instance_secret_lookup" to table: "instance_secret"
CREATE UNIQUE INDEX `instance_secret_lookup` ON `instance_secret` (`instance_name`, `secret_name`);
-- create "app_secret" table
CREATE TABLE `app_secret` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `secret_name` text NOT NULL,
  `encrypted_value` bytea NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "app_secret_name" to table: "app_secret"
CREATE UNIQUE INDEX `app_secret_name` ON `app_secret` (`secret_name`);
-- create "external_service_config" table
CREATE TABLE `external_service_config` (
  `service_name` text NOT NULL,
  `api_key_secret_id` integer NULL,
  `enabled` boolean NOT NULL DEFAULT false,
  `proxy_url` text NULL,
  `proxy_user` text NULL,
  `proxy_pass_secret_id` integer NULL,
  `last4` text NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`service_name`),
  CONSTRAINT `external_service_config_proxy_pass_secret_id_fkey` FOREIGN KEY (`proxy_pass_secret_id`) REFERENCES `app_secret` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT `external_service_config_api_key_secret_id_fkey` FOREIGN KEY (`api_key_secret_id`) REFERENCES `app_secret` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL
);
-- create "external_service_quota_state" table
CREATE TABLE `external_service_quota_state` (
  `service_name` text NOT NULL,
  `window_start` datetime NOT NULL,
  `requests_made` integer NOT NULL DEFAULT 0,
  `requests_quota` integer NOT NULL DEFAULT 0,
  `exhausted_at` datetime NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`service_name`, `window_start`)
);
-- create index "external_service_quota_state_window" to table: "external_service_quota_state"
CREATE INDEX `external_service_quota_state_window` ON `external_service_quota_state` (`window_start`);
