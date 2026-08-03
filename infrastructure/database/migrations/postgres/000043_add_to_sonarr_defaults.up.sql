-- ADR-0009 S6 — per-instance Add-to-Sonarr defaults (see sqlite up for the full
-- rationale). Two nullable hint columns on sonarr_instance_settings. NULL = no
-- default set. Soft references (not FK-checked); re-validated against the live
-- Sonarr list at read time. No backfill — every existing row starts NULL.
ALTER TABLE "sonarr_instance_settings" ADD COLUMN "default_quality_profile_id" integer NULL;
ALTER TABLE "sonarr_instance_settings" ADD COLUMN "default_root_folder_path" text NULL;
