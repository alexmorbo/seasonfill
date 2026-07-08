-- Re-add people.name NULLABLE (rollback safety net; data backfill from
-- people_texts en-US is a separate manual op). SQLite supports a plain
-- ADD COLUMN for a nullable column without a table rebuild (matches the
-- 000028 precedent) — deviation from the raw atlas-generated down, which
-- referenced the already-renamed `new_people` table and would fail at
-- apply time (see story 1084b implementation notes).
ALTER TABLE `people` ADD COLUMN `name` text NULL;
