-- reverse: create index "download_links_external_series_idx" to table: "download_links"
DROP INDEX `download_links_external_series_idx`;
-- reverse: create index "download_links_instance_source_idx" to table: "download_links"
DROP INDEX `download_links_instance_source_idx`;
-- reverse: create index "download_links_global_series_idx" to table: "download_links"
DROP INDEX `download_links_global_series_idx`;
-- reverse: create "download_links" table
DROP TABLE `download_links`;
-- reverse: create index "episode_grabs_episode_idx" to table: "episode_grabs"
DROP INDEX `episode_grabs_episode_idx`;
-- reverse: create "episode_grabs" table
DROP TABLE `episode_grabs`;
-- reverse: create index "grab_records_replay_of_idx" to table: "grab_records"
DROP INDEX `grab_records_replay_of_idx`;
-- reverse: create index "grab_records_inst_created_idx" to table: "grab_records"
DROP INDEX `grab_records_inst_created_idx`;
-- reverse: create index "grab_records_status_idx" to table: "grab_records"
DROP INDEX `grab_records_status_idx`;
-- reverse: create index "grab_records_scan_run_idx" to table: "grab_records"
DROP INDEX `grab_records_scan_run_idx`;
-- reverse: create index "grab_records_download_id_idx" to table: "grab_records"
DROP INDEX `grab_records_download_id_idx`;
-- reverse: create index "grab_records_release_guid_idx" to table: "grab_records"
DROP INDEX `grab_records_release_guid_idx`;
-- reverse: create index "grab_records_dedupe_lookup_idx" to table: "grab_records"
DROP INDEX `grab_records_dedupe_lookup_idx`;
-- reverse: create index "grab_records_inst_series_idx" to table: "grab_records"
DROP INDEX `grab_records_inst_series_idx`;
-- reverse: create "grab_records" table
DROP TABLE `grab_records`;
