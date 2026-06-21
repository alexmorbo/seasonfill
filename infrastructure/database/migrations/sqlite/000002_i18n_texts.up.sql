-- create "series_texts" table
CREATE TABLE `series_texts` (
  `series_id` integer NOT NULL,
  `language` text NOT NULL,
  `title` text NULL,
  `overview` text NULL,
  `tagline` text NULL,
  `enriched_at` datetime NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`series_id`, `language`),
  CONSTRAINT `series_texts_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create "episode_texts" table
CREATE TABLE `episode_texts` (
  `episode_id` integer NOT NULL,
  `language` text NOT NULL,
  `title` text NULL,
  `overview` text NULL,
  `enriched_at` datetime NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`episode_id`, `language`),
  CONSTRAINT `episode_texts_episode_id_fkey` FOREIGN KEY (`episode_id`) REFERENCES `episodes` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
