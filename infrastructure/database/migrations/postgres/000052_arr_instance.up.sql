-- Ф6-R-1: generalize the shared instance table to serve Sonarr + (future)
-- Radarr. Pure rename + additive `type` column + empty radarr settings
-- sibling. No behavior change. See story R-1 / F-10 deploy gate.

-- rename parent table (child FKs auto-repoint to arr_instance by OID)
ALTER TABLE "sonarr_instance" RENAME TO "arr_instance";
-- additive discriminator; existing rows backfill to 'sonarr'
ALTER TABLE "arr_instance" ADD COLUMN "type" text NOT NULL DEFAULT 'sonarr';
-- rename the two objects whose NAME embeds the old parent name
ALTER TABLE "arr_instance" RENAME CONSTRAINT "sonarr_instance_token_secret_id_fkey" TO "arr_instance_token_secret_id_fkey";
ALTER INDEX "sonarr_instance_unhealthy" RENAME TO "arr_instance_unhealthy";
-- empty radarr settings sibling (mirrors sonarr_instance_settings shape)
CREATE TABLE "radarr_instance_settings" (
  "instance_name" text NOT NULL,
  "timeout_seconds" integer NOT NULL DEFAULT 10,
  "search_timeout_seconds" integer NOT NULL DEFAULT 60,
  "dry_run" boolean NULL,
  "tags_mode" text NOT NULL DEFAULT 'any',
  "tags_include" text NOT NULL DEFAULT '',
  "tags_exclude" text NOT NULL DEFAULT '',
  "search_require_all_aired" boolean NOT NULL DEFAULT false,
  "search_skip_specials" boolean NOT NULL DEFAULT true,
  "search_skip_anime" boolean NOT NULL DEFAULT false,
  "search_min_custom_format_score" integer NOT NULL DEFAULT 0,
  "ranking_indexer_priority_enabled" boolean NOT NULL DEFAULT false,
  "ranking_origin_bonus" double precision NOT NULL DEFAULT 0,
  "limits_scan_max_series" integer NOT NULL DEFAULT 0,
  "limits_max_grabs_per_scan" integer NOT NULL DEFAULT 0,
  "rate_limit_rpm" integer NOT NULL DEFAULT 30,
  "rate_limit_burst" integer NOT NULL DEFAULT 10,
  "cooldown_mode" text NOT NULL DEFAULT '',
  "cooldown_series_after_grab_sec" integer NOT NULL DEFAULT 0,
  "cooldown_guid_failed_grab_sec" integer NOT NULL DEFAULT 0,
  "cooldown_guid_failed_import_sec" integer NOT NULL DEFAULT 0,
  "retry_max_attempts" integer NOT NULL DEFAULT 0,
  "retry_initial_backoff_sec" integer NOT NULL DEFAULT 0,
  "retry_max_backoff_sec" integer NOT NULL DEFAULT 0,
  "healthcheck_recheck_auth_sec" integer NOT NULL DEFAULT 0,
  "healthcheck_recheck_net_sec" integer NOT NULL DEFAULT 0,
  "public_url" text NULL,
  "webhook_install_enabled" boolean NOT NULL DEFAULT true,
  "webhook_url_override" text NULL,
  "parse_on_grab_enabled" boolean NOT NULL DEFAULT true,
  "scan_skip_handled_seasons" boolean NOT NULL DEFAULT true,
  "default_quality_profile_id" integer NULL,
  "default_root_folder_path" text NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("instance_name"),
  CONSTRAINT "radarr_instance_settings_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "arr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE
);
