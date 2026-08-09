-- create "followed_series" table
CREATE TABLE `followed_series` (
  `series_id` integer NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`series_id`),
  CONSTRAINT `followed_series_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
