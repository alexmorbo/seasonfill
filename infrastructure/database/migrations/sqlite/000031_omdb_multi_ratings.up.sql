-- add column "omdb_rt_rating" to table: "series"
ALTER TABLE `series` ADD COLUMN `omdb_rt_rating` integer NULL;
-- add column "omdb_metacritic" to table: "series"
ALTER TABLE `series` ADD COLUMN `omdb_metacritic` integer NULL;
