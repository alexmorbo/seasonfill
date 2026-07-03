-- Reviewed destructive migration (S-E3b): the localizable canon columns
-- title / poster_asset / backdrop_asset (series) and name / overview /
-- poster_asset (seasons) moved to the series_texts / series_media_texts /
-- season_texts / season_media_texts side tables (S-C / S-C2 / 584a). All
-- readers were repointed in S-E3a. Down re-adds them NULLABLE (backfill
-- from the texts en-US tier is a separate manual op).
-- modify "seasons" table
-- atlas:nolint destructive
ALTER TABLE "seasons" DROP COLUMN "name", DROP COLUMN "overview", DROP COLUMN "poster_asset";
-- modify "series" table
-- atlas:nolint destructive
ALTER TABLE "series" DROP COLUMN "title", DROP COLUMN "poster_asset", DROP COLUMN "backdrop_asset";
