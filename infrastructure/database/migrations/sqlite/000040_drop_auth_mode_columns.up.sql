-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_app_config" table
CREATE TABLE `new_app_config` (
  `id` integer NOT NULL DEFAULT 1,
  `cron_enabled` boolean NOT NULL DEFAULT true,
  `cron_schedule` text NOT NULL DEFAULT '0 */6 * * *',
  `cron_on_start` boolean NOT NULL DEFAULT false,
  `cron_jitter_seconds` integer NOT NULL DEFAULT 60,
  `scan_shutdown_grace_sec` integer NOT NULL DEFAULT 60,
  `scan_cooldown_sweep_sec` integer NOT NULL DEFAULT 900,
  `dry_run` boolean NOT NULL DEFAULT true,
  `global_rpm` integer NOT NULL DEFAULT 30,
  `global_burst` integer NOT NULL DEFAULT 10,
  `auth_session_ttl_sec` integer NOT NULL DEFAULT 43200,
  `auth_secure_cookie` boolean NOT NULL DEFAULT false,
  `auth_trusted_proxies` text NOT NULL DEFAULT '[]',
  `auth_session_epoch` bigint NOT NULL DEFAULT 0,
  `oidc_issuer` text NOT NULL DEFAULT '',
  `oidc_client_id` text NOT NULL DEFAULT '',
  `oidc_redirect_url` text NOT NULL DEFAULT '',
  `oidc_scopes` text NOT NULL DEFAULT '[]',
  `oidc_username_claim` text NOT NULL DEFAULT '',
  `oidc_allowed_groups` text NOT NULL DEFAULT '[]',
  `oidc_groups_claim` text NOT NULL DEFAULT 'groups',
  `guid_rewrites` text NOT NULL DEFAULT '[]',
  `api_key_auto_generated` boolean NOT NULL DEFAULT false,
  `timezone` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`id`),
  CONSTRAINT `app_config_singleton` CHECK (id = 1)
);
-- copy rows from old table "app_config" to new temporary table "new_app_config"
INSERT INTO `new_app_config` (`id`, `cron_enabled`, `cron_schedule`, `cron_on_start`, `cron_jitter_seconds`, `scan_shutdown_grace_sec`, `scan_cooldown_sweep_sec`, `dry_run`, `global_rpm`, `global_burst`, `auth_session_ttl_sec`, `auth_secure_cookie`, `auth_trusted_proxies`, `auth_session_epoch`, `oidc_issuer`, `oidc_client_id`, `oidc_redirect_url`, `oidc_scopes`, `oidc_username_claim`, `oidc_allowed_groups`, `oidc_groups_claim`, `guid_rewrites`, `api_key_auto_generated`, `timezone`, `created_at`, `updated_at`) SELECT `id`, `cron_enabled`, `cron_schedule`, `cron_on_start`, `cron_jitter_seconds`, `scan_shutdown_grace_sec`, `scan_cooldown_sweep_sec`, `dry_run`, `global_rpm`, `global_burst`, `auth_session_ttl_sec`, `auth_secure_cookie`, `auth_trusted_proxies`, `auth_session_epoch`, `oidc_issuer`, `oidc_client_id`, `oidc_redirect_url`, `oidc_scopes`, `oidc_username_claim`, `oidc_allowed_groups`, `oidc_groups_claim`, `guid_rewrites`, `api_key_auto_generated`, `timezone`, `created_at`, `updated_at` FROM `app_config`;
-- drop "app_config" table after copying rows
DROP TABLE `app_config`;
-- rename temporary table "new_app_config" to "app_config"
ALTER TABLE `new_app_config` RENAME TO `app_config`;
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
-- AR-4: invalidate all existing session cookies (auth surface changed).
UPDATE `app_config` SET `auth_session_epoch` = `auth_session_epoch` + 1;
