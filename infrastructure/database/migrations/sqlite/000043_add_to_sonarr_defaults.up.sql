-- ADR-0009 S6 — per-instance Add-to-Sonarr defaults. Two nullable hints on
-- sonarr_instance_settings that pre-fill the "Add to Sonarr" modal when an
-- instance is chosen: default quality-profile id (int) + default root-folder
-- path (text). NULL = no default set → empty pre-fill. Soft references, never
-- FK-checked against Sonarr; re-validated against the live Sonarr list at read
-- time (a default that vanished in Sonarr silently pre-fills empty). No
-- backfill — every existing row starts NULL.
ALTER TABLE `sonarr_instance_settings` ADD COLUMN `default_quality_profile_id` integer NULL;
ALTER TABLE `sonarr_instance_settings` ADD COLUMN `default_root_folder_path` text NULL;
