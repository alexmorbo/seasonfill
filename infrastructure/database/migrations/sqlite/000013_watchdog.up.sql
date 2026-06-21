-- create "watchdog_state" table
CREATE TABLE `watchdog_state` (
  `instance_name` text NOT NULL,
  `sonarr_series_id` bigint NOT NULL,
  `season_number` integer NOT NULL,
  `attempt_count` integer NOT NULL DEFAULT 0,
  `last_attempt_at` datetime NOT NULL,
  `cooldown_until` datetime NULL,
  `last_error` text NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`instance_name`, `sonarr_series_id`, `season_number`),
  CONSTRAINT `watchdog_state_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "watchdog_state_instance_name_idx" to table: "watchdog_state"
CREATE INDEX `watchdog_state_instance_name_idx` ON `watchdog_state` (`instance_name`);
-- create index "watchdog_state_cooldown_until_idx" to table: "watchdog_state"
CREATE INDEX `watchdog_state_cooldown_until_idx` ON `watchdog_state` (`cooldown_until`) WHERE cooldown_until IS NOT NULL;
-- create "watchdog_blacklist" table
CREATE TABLE `watchdog_blacklist` (
  `instance_name` text NOT NULL,
  `sonarr_series_id` bigint NOT NULL,
  `season_number` integer NOT NULL,
  `release_title` text NULL,
  `reason` text NOT NULL,
  `consecutive` integer NOT NULL DEFAULT 0,
  `blacklisted_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `ttl_until` datetime NULL,
  PRIMARY KEY (`instance_name`, `sonarr_series_id`, `season_number`),
  CONSTRAINT `watchdog_blacklist_instance_name_fkey` FOREIGN KEY (`instance_name`) REFERENCES `sonarr_instance` (`name`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "watchdog_blacklist_ttl_until_idx" to table: "watchdog_blacklist"
CREATE INDEX `watchdog_blacklist_ttl_until_idx` ON `watchdog_blacklist` (`ttl_until`) WHERE ttl_until IS NOT NULL;
