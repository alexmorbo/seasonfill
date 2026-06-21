-- reverse: create index "watchdog_blacklist_ttl_until_idx" to table: "watchdog_blacklist"
DROP INDEX `watchdog_blacklist_ttl_until_idx`;
-- reverse: create "watchdog_blacklist" table
DROP TABLE `watchdog_blacklist`;
-- reverse: create index "watchdog_state_cooldown_until_idx" to table: "watchdog_state"
DROP INDEX `watchdog_state_cooldown_until_idx`;
-- reverse: create index "watchdog_state_instance_name_idx" to table: "watchdog_state"
DROP INDEX `watchdog_state_instance_name_idx`;
-- reverse: create "watchdog_state" table
DROP TABLE `watchdog_state`;
