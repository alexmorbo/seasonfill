-- create "genres" table
CREATE TABLE `genres` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_id` integer NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "genres_tmdb_id" to table: "genres"
CREATE UNIQUE INDEX `genres_tmdb_id` ON `genres` (`tmdb_id`) WHERE tmdb_id IS NOT NULL;
-- create "networks" table
CREATE TABLE `networks` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_id` integer NULL,
  `name` text NOT NULL,
  `logo_asset` text NULL,
  `origin_country` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "networks_tmdb_id" to table: "networks"
CREATE UNIQUE INDEX `networks_tmdb_id` ON `networks` (`tmdb_id`) WHERE tmdb_id IS NOT NULL;
-- create "production_companies" table
CREATE TABLE `production_companies` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_id` integer NULL,
  `name` text NOT NULL,
  `logo_asset` text NULL,
  `origin_country` text NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "production_companies_tmdb_id" to table: "production_companies"
CREATE UNIQUE INDEX `production_companies_tmdb_id` ON `production_companies` (`tmdb_id`) WHERE tmdb_id IS NOT NULL;
-- create "keywords" table
CREATE TABLE `keywords` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `tmdb_id` integer NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);
-- create index "keywords_tmdb_id" to table: "keywords"
CREATE UNIQUE INDEX `keywords_tmdb_id` ON `keywords` (`tmdb_id`) WHERE tmdb_id IS NOT NULL;
-- create "genres_i18n" table
CREATE TABLE `genres_i18n` (
  `genre_id` integer NOT NULL,
  `language` text NOT NULL,
  `name` text NOT NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`genre_id`, `language`),
  CONSTRAINT `genres_i18n_genre_id_fkey` FOREIGN KEY (`genre_id`) REFERENCES `genres` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "genres_i18n_name" to table: "genres_i18n"
CREATE INDEX `genres_i18n_name` ON `genres_i18n` (`language`, `name`);
-- create "networks_i18n" table
CREATE TABLE `networks_i18n` (
  `network_id` integer NOT NULL,
  `language` text NOT NULL,
  `name` text NOT NULL,
  `description` text NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`network_id`, `language`),
  CONSTRAINT `networks_i18n_network_id_fkey` FOREIGN KEY (`network_id`) REFERENCES `networks` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "networks_i18n_name" to table: "networks_i18n"
CREATE INDEX `networks_i18n_name` ON `networks_i18n` (`language`, `name`);
-- create "production_companies_i18n" table
CREATE TABLE `production_companies_i18n` (
  `company_id` integer NOT NULL,
  `language` text NOT NULL,
  `name` text NOT NULL,
  `description` text NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`company_id`, `language`),
  CONSTRAINT `production_companies_i18n_company_id_fkey` FOREIGN KEY (`company_id`) REFERENCES `production_companies` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "production_companies_i18n_name" to table: "production_companies_i18n"
CREATE INDEX `production_companies_i18n_name` ON `production_companies_i18n` (`language`, `name`);
-- create "keywords_i18n" table
CREATE TABLE `keywords_i18n` (
  `keyword_id` integer NOT NULL,
  `language` text NOT NULL,
  `name` text NOT NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`keyword_id`, `language`),
  CONSTRAINT `keywords_i18n_keyword_id_fkey` FOREIGN KEY (`keyword_id`) REFERENCES `keywords` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "keywords_i18n_name" to table: "keywords_i18n"
CREATE INDEX `keywords_i18n_name` ON `keywords_i18n` (`language`, `name`);
