-- reverse: add column "tmdb_rating" to table: "discovery_lists"
ALTER TABLE `discovery_lists` DROP COLUMN `tmdb_rating`;
-- reverse: add column "year" to table: "discovery_lists"
ALTER TABLE `discovery_lists` DROP COLUMN `year`;
