-- modify "app_config" table
ALTER TABLE "app_config" ADD COLUMN "ics_epoch" bigint NOT NULL DEFAULT 0;
