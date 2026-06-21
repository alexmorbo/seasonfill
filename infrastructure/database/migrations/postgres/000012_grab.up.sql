-- create "download_links" table
CREATE TABLE "download_links" (
  "qbit_hash" text NOT NULL,
  "instance_name" text NOT NULL,
  "instance_type" text NOT NULL DEFAULT 'sonarr',
  "external_series_id" bigint NULL,
  "external_movie_id" bigint NULL,
  "external_episode_ids" text NULL,
  "global_series_id" bigint NULL,
  "discovered_at" timestamptz NOT NULL DEFAULT now(),
  "source" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("qbit_hash"),
  CONSTRAINT "download_links_global_series_id_fkey" FOREIGN KEY ("global_series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "download_links_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "sonarr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "download_links_instance_type_check" CHECK (instance_type = ANY (ARRAY['sonarr'::text, 'radarr'::text])),
  CONSTRAINT "download_links_source_check" CHECK (source = ANY (ARRAY['webhook'::text, 'arr-poll'::text, 'instance-backfill'::text])),
  CONSTRAINT "download_links_type_id_check" CHECK (((instance_type = 'sonarr'::text) AND (external_series_id IS NOT NULL) AND (external_movie_id IS NULL)) OR ((instance_type = 'radarr'::text) AND (external_movie_id IS NOT NULL) AND (external_series_id IS NULL)))
);
-- create index "download_links_external_series_idx" to table: "download_links"
CREATE INDEX "download_links_external_series_idx" ON "download_links" ("instance_name", "external_series_id");
-- create index "download_links_global_series_idx" to table: "download_links"
CREATE INDEX "download_links_global_series_idx" ON "download_links" ("global_series_id");
-- create index "download_links_instance_source_idx" to table: "download_links"
CREATE INDEX "download_links_instance_source_idx" ON "download_links" ("instance_name", "source");
-- create "grab_records" table
CREATE TABLE "grab_records" (
  "id" text NOT NULL,
  "instance_name" text NOT NULL,
  "series_id" bigint NOT NULL,
  "series_title" text NULL,
  "season_number" integer NOT NULL,
  "release_guid" text NULL,
  "release_title" text NULL,
  "download_id" text NULL,
  "indexer_id" integer NULL,
  "indexer_name" text NULL,
  "custom_format_score" integer NOT NULL DEFAULT 0,
  "quality" text NULL,
  "coverage_count" integer NOT NULL DEFAULT 0,
  "status" text NOT NULL DEFAULT 'grabbed',
  "error_message" text NULL,
  "scan_run_id" text NULL,
  "attempts" integer NOT NULL DEFAULT 0,
  "torrent_hash" text NULL,
  "replay_of_id" text NULL,
  "size_bytes" bigint NULL,
  "parsed_codec" text NULL,
  "parsed_source" text NULL,
  "parsed_quality" text NULL,
  "parsed_resolution" integer NULL,
  "parsed_hdr_flags" text NULL,
  "parsed_dub" text NULL,
  "parsed_languages" text NULL,
  "parsed_subs" text NULL,
  "parsed_release_group" text NULL,
  "parsed_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "grab_records_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "sonarr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "grab_records_status_check" CHECK (status = ANY (ARRAY['grabbed'::text, 'grab_failed'::text, 'imported'::text, 'import_failed'::text]))
);
-- create index "grab_records_dedupe_lookup_idx" to table: "grab_records"
CREATE INDEX "grab_records_dedupe_lookup_idx" ON "grab_records" ("instance_name", "series_id", "season_number", "release_guid");
-- create index "grab_records_download_id_idx" to table: "grab_records"
CREATE INDEX "grab_records_download_id_idx" ON "grab_records" ("download_id");
-- create index "grab_records_inst_created_idx" to table: "grab_records"
CREATE INDEX "grab_records_inst_created_idx" ON "grab_records" ("instance_name", "created_at");
-- create index "grab_records_inst_series_idx" to table: "grab_records"
CREATE INDEX "grab_records_inst_series_idx" ON "grab_records" ("instance_name", "series_id", "season_number");
-- create index "grab_records_release_guid_idx" to table: "grab_records"
CREATE INDEX "grab_records_release_guid_idx" ON "grab_records" ("release_guid");
-- create index "grab_records_replay_of_idx" to table: "grab_records"
CREATE INDEX "grab_records_replay_of_idx" ON "grab_records" ("replay_of_id") WHERE (replay_of_id IS NOT NULL);
-- create index "grab_records_scan_run_idx" to table: "grab_records"
CREATE INDEX "grab_records_scan_run_idx" ON "grab_records" ("scan_run_id");
-- create index "grab_records_status_idx" to table: "grab_records"
CREATE INDEX "grab_records_status_idx" ON "grab_records" ("status");
-- create "episode_grabs" table
CREATE TABLE "episode_grabs" (
  "grab_id" text NOT NULL,
  "episode_id" bigint NOT NULL,
  "episode_number" integer NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("grab_id", "episode_id"),
  CONSTRAINT "episode_grabs_episode_id_fkey" FOREIGN KEY ("episode_id") REFERENCES "episodes" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "episode_grabs_grab_id_fkey" FOREIGN KEY ("grab_id") REFERENCES "grab_records" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "episode_grabs_episode_idx" to table: "episode_grabs"
CREATE INDEX "episode_grabs_episode_idx" ON "episode_grabs" ("episode_id");
