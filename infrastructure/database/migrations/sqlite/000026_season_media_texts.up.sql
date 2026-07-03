-- create "season_media_texts" table
CREATE TABLE `season_media_texts` (
  `series_id` integer NOT NULL,
  `season_number` integer NOT NULL,
  `language` text NOT NULL,
  `poster_asset` text NULL,
  `poster_hash` text NULL,
  `backdrop_asset` text NULL,
  `backdrop_hash` text NULL,
  `enriched_at` datetime NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`series_id`, `season_number`, `language`),
  CONSTRAINT `season_media_texts_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
