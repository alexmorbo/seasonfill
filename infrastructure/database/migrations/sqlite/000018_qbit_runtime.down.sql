-- reverse: create index "torrent_series_map_series_idx" to table: "torrent_series_map"
DROP INDEX `torrent_series_map_series_idx`;
-- reverse: create "torrent_series_map" table
DROP TABLE `torrent_series_map`;
-- reverse: create index "qbit_torrent_events_instance_hash_idx" to table: "qbit_torrent_events"
DROP INDEX `qbit_torrent_events_instance_hash_idx`;
-- reverse: create index "qbit_torrent_events_occurred_at_idx" to table: "qbit_torrent_events"
DROP INDEX `qbit_torrent_events_occurred_at_idx`;
-- reverse: create "qbit_torrent_events" table
DROP TABLE `qbit_torrent_events`;
-- reverse: create index "qbit_torrents_state_group_idx" to table: "qbit_torrents"
DROP INDEX `qbit_torrents_state_group_idx`;
-- reverse: create "qbit_torrents" table
DROP TABLE `qbit_torrents`;
-- reverse: create "qbit_settings" table
DROP TABLE `qbit_settings`;
