-- create "series_genres" table
CREATE TABLE `series_genres` (
  `series_id` integer NOT NULL,
  `genre_id` integer NOT NULL,
  `position` integer NULL,
  PRIMARY KEY (`series_id`, `genre_id`),
  CONSTRAINT `series_genres_genre_id_fkey` FOREIGN KEY (`genre_id`) REFERENCES `genres` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT `series_genres_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "series_genres_genre" to table: "series_genres"
CREATE INDEX `series_genres_genre` ON `series_genres` (`genre_id`);
-- create "series_networks" table
CREATE TABLE `series_networks` (
  `series_id` integer NOT NULL,
  `network_id` integer NOT NULL,
  `position` integer NULL,
  PRIMARY KEY (`series_id`, `network_id`),
  CONSTRAINT `series_networks_network_id_fkey` FOREIGN KEY (`network_id`) REFERENCES `networks` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT `series_networks_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "series_networks_network" to table: "series_networks"
CREATE INDEX `series_networks_network` ON `series_networks` (`network_id`);
-- create "series_companies" table
CREATE TABLE `series_companies` (
  `series_id` integer NOT NULL,
  `company_id` integer NOT NULL,
  `position` integer NULL,
  PRIMARY KEY (`series_id`, `company_id`),
  CONSTRAINT `series_companies_company_id_fkey` FOREIGN KEY (`company_id`) REFERENCES `production_companies` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT `series_companies_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "series_companies_company" to table: "series_companies"
CREATE INDEX `series_companies_company` ON `series_companies` (`company_id`);
-- create "series_keywords" table
CREATE TABLE `series_keywords` (
  `series_id` integer NOT NULL,
  `keyword_id` integer NOT NULL,
  PRIMARY KEY (`series_id`, `keyword_id`),
  CONSTRAINT `series_keywords_keyword_id_fkey` FOREIGN KEY (`keyword_id`) REFERENCES `keywords` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT `series_keywords_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "series_keywords_keyword" to table: "series_keywords"
CREATE INDEX `series_keywords_keyword` ON `series_keywords` (`keyword_id`);
