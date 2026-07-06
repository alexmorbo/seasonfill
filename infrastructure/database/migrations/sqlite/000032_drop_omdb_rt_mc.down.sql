-- reverse: drop column "omdb_rt_rating" from table: "series"
ALTER TABLE `series` ADD COLUMN `omdb_rt_rating` integer NULL;
-- reverse: drop column "omdb_metacritic" from table: "series"
ALTER TABLE `series` ADD COLUMN `omdb_metacritic` integer NULL;
