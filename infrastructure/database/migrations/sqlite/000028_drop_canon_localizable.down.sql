-- Re-add the localizable canon columns NULLABLE (rollback safety net;
-- data backfill from the *_texts en-US tier is a separate manual op).
-- The up used a table rebuild, so wrap in the PRAGMA foreign_keys guard.
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
ALTER TABLE `series` ADD COLUMN `title` text NULL;
ALTER TABLE `series` ADD COLUMN `poster_asset` text NULL;
ALTER TABLE `series` ADD COLUMN `backdrop_asset` text NULL;
ALTER TABLE `seasons` ADD COLUMN `name` text NULL;
ALTER TABLE `seasons` ADD COLUMN `overview` text NULL;
ALTER TABLE `seasons` ADD COLUMN `poster_asset` text NULL;
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
