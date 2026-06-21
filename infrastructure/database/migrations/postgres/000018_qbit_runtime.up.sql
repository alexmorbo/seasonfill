-- create "qbit_settings" table
CREATE TABLE "qbit_settings" (
  "instance_name" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT false,
  "url" text NOT NULL,
  "username" text NULL,
  "password_encrypted" bytea NULL,
  "category" text NOT NULL DEFAULT 'sonarr',
  "poll_interval_minutes" integer NOT NULL DEFAULT 30,
  "regrab_cooldown_hours" integer NOT NULL DEFAULT 120,
  "max_consecutive_no_better" integer NOT NULL DEFAULT 3,
  "custom_unregistered_msgs" jsonb NOT NULL DEFAULT '[]',
  "qbit_public_url" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("instance_name"),
  CONSTRAINT "qbit_settings_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "sonarr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create "qbit_torrent_events" table
CREATE TABLE "qbit_torrent_events" (
  "id" bigserial NOT NULL,
  "instance_name" text NOT NULL,
  "torrent_hash" text NOT NULL,
  "event" text NOT NULL,
  "from_group" text NULL,
  "to_group" text NULL,
  "occurred_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "qbit_torrent_events_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "sonarr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "qbit_torrent_events_instance_hash_idx" to table: "qbit_torrent_events"
CREATE INDEX "qbit_torrent_events_instance_hash_idx" ON "qbit_torrent_events" ("instance_name", "torrent_hash");
-- create index "qbit_torrent_events_occurred_at_idx" to table: "qbit_torrent_events"
CREATE INDEX "qbit_torrent_events_occurred_at_idx" ON "qbit_torrent_events" ("occurred_at");
-- create "qbit_torrents" table
CREATE TABLE "qbit_torrents" (
  "instance_name" text NOT NULL,
  "hash" text NOT NULL,
  "infohash_v2" text NULL,
  "name" text NOT NULL,
  "category" text NULL,
  "tags" text NULL,
  "tracker_host" text NULL,
  "save_path" text NULL,
  "content_path" text NULL,
  "state_raw" text NOT NULL,
  "state_group" text NOT NULL,
  "size_bytes" bigint NOT NULL DEFAULT 0,
  "total_size" bigint NOT NULL DEFAULT 0,
  "downloaded" bigint NOT NULL DEFAULT 0,
  "uploaded" bigint NOT NULL DEFAULT 0,
  "ratio" double precision NOT NULL DEFAULT 0,
  "popularity" double precision NOT NULL DEFAULT 0,
  "time_active_s" bigint NOT NULL DEFAULT 0,
  "seeding_time_s" bigint NOT NULL DEFAULT 0,
  "added_on" timestamptz NULL,
  "completion_on" timestamptz NULL,
  "last_activity" timestamptz NULL,
  "season_number" integer NULL,
  "present" boolean NOT NULL DEFAULT true,
  "deleted_at" timestamptz NULL,
  "first_seen_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("instance_name", "hash"),
  CONSTRAINT "qbit_torrents_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "sonarr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "qbit_torrents_state_group_idx" to table: "qbit_torrents"
CREATE INDEX "qbit_torrents_state_group_idx" ON "qbit_torrents" ("instance_name", "state_group");
-- create "torrent_series_map" table
CREATE TABLE "torrent_series_map" (
  "instance_name" text NOT NULL,
  "torrent_hash" text NOT NULL,
  "series_id" bigint NOT NULL,
  "season_number" integer NULL,
  "source" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("instance_name", "torrent_hash"),
  CONSTRAINT "torrent_series_map_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "sonarr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "torrent_series_map_series_idx" to table: "torrent_series_map"
CREATE INDEX "torrent_series_map_series_idx" ON "torrent_series_map" ("instance_name", "series_id");
