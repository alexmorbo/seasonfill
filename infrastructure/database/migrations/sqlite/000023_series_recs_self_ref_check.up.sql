-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_series_recommendations" table
CREATE TABLE `new_series_recommendations` (
  `series_id` integer NOT NULL,
  `recommended_series_id` integer NOT NULL,
  `position` integer NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`series_id`, `recommended_series_id`),
  CONSTRAINT `series_recommendations_recommended_series_id_fkey` FOREIGN KEY (`recommended_series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `series_recommendations_series_id_fkey` FOREIGN KEY (`series_id`) REFERENCES `series` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `series_recommendations_no_self_ref` CHECK (recommended_series_id != series_id)
);
-- copy rows from old table "series_recommendations" to new temporary table "new_series_recommendations"
INSERT INTO `new_series_recommendations` (`series_id`, `recommended_series_id`, `position`, `updated_at`) SELECT `series_id`, `recommended_series_id`, `position`, `updated_at` FROM `series_recommendations`;
-- drop "series_recommendations" table after copying rows
DROP TABLE `series_recommendations`;
-- rename temporary table "new_series_recommendations" to "series_recommendations"
ALTER TABLE `new_series_recommendations` RENAME TO `series_recommendations`;
-- create index "series_recommendations_position" to table: "series_recommendations"
CREATE INDEX `series_recommendations_position` ON `series_recommendations` (`series_id`, `position`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
