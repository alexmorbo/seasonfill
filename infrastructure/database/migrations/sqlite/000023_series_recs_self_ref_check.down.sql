-- E-1 A3b — reverse of 000023 series_recommendations CHECK constraint on SQLite.
--
-- Atlas's auto-generated `.down.sql` was incorrect (it referenced the transient
-- `new_series_recommendations` table that no longer exists at runtime — the
-- up migration renamed it to `series_recommendations`). This hand-rolled
-- mirror recreates the original pre-CHECK shape.

-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_series_recommendations" mirroring the original pre-CHECK shape
CREATE TABLE `new_series_recommendations` (
  `series_id` integer NOT NULL,
  `recommended_series_id` integer NOT NULL,
  `position` integer NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`series_id`, `recommended_series_id`),
  CONSTRAINT `series_recommendations_recommended_series_id_fkey` FOREIGN KEY (`recommended_series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `series_recommendations_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- copy rows from current "series_recommendations" to temporary table
INSERT INTO `new_series_recommendations` (`series_id`, `recommended_series_id`, `position`, `updated_at`) SELECT `series_id`, `recommended_series_id`, `position`, `updated_at` FROM `series_recommendations`;
-- drop existing (WITH check) "series_recommendations"
DROP TABLE `series_recommendations`;
-- rename temporary table to original name
ALTER TABLE `new_series_recommendations` RENAME TO `series_recommendations`;
-- recreate the position index
CREATE INDEX `series_recommendations_position` ON `series_recommendations` (`series_id`, `position`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
