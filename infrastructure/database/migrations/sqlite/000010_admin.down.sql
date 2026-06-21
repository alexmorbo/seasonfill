-- reverse: create index "external_service_quota_state_window" to table: "external_service_quota_state"
DROP INDEX `external_service_quota_state_window`;
-- reverse: create "external_service_quota_state" table
DROP TABLE `external_service_quota_state`;
-- reverse: create "external_service_config" table
DROP TABLE `external_service_config`;
-- reverse: create index "app_secret_name" to table: "app_secret"
DROP INDEX `app_secret_name`;
-- reverse: create "app_secret" table
DROP TABLE `app_secret`;
-- reverse: create index "instance_secret_lookup" to table: "instance_secret"
DROP INDEX `instance_secret_lookup`;
-- reverse: create "instance_secret" table
DROP TABLE `instance_secret`;
-- reverse: create index "sonarr_instance_unhealthy" to table: "sonarr_instance"
DROP INDEX `sonarr_instance_unhealthy`;
-- reverse: create "sonarr_instance" table
DROP TABLE `sonarr_instance`;
