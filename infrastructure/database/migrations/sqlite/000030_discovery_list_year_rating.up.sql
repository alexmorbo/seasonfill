-- add column "year" to table: "discovery_lists"
ALTER TABLE `discovery_lists` ADD COLUMN `year` integer NULL;
-- add column "tmdb_rating" to table: "discovery_lists"
ALTER TABLE `discovery_lists` ADD COLUMN `tmdb_rating` double precision NULL;
