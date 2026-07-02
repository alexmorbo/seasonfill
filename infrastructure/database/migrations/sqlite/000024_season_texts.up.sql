-- create "season_texts" table
CREATE TABLE `season_texts` (
  `series_id` integer NOT NULL,
  `season_number` integer NOT NULL,
  `language` text NOT NULL,
  `name` text NULL,
  `overview` text NULL,
  `enriched_at` datetime NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`series_id`, `season_number`, `language`),
  CONSTRAINT `season_texts_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
