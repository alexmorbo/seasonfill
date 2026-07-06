-- Reviewed destructive migration (W18-0 / decision 2026-07-06): OMDb returns
-- Rotten Tomatoes / Metacritic only for type=movie, never TV series. seasonfill
-- is TV-only, so series.omdb_rt_rating / series.omdb_metacritic are NULL for the
-- entire library forever. Dropping them is intentional; the source-agnostic
-- fetch/parse layer (omdb client Ratings[] + mapper parseRTRating/parseMetacritic)
-- is retained for future movie support. Down-migration re-adds both as nullable.
-- atlas:nolint destructive
-- drop column "omdb_metacritic" from table: "series"
ALTER TABLE `series` DROP COLUMN `omdb_metacritic`;
-- atlas:nolint destructive
-- drop column "omdb_rt_rating" from table: "series"
ALTER TABLE `series` DROP COLUMN `omdb_rt_rating`;
