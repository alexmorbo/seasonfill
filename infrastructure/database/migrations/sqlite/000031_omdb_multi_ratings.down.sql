-- reverse: add column "omdb_metacritic" to table: "series"
ALTER TABLE `series` DROP COLUMN `omdb_metacritic`;
-- reverse: add column "omdb_rt_rating" to table: "series"
ALTER TABLE `series` DROP COLUMN `omdb_rt_rating`;
