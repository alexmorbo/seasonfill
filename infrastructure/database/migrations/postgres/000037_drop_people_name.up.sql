-- Story 1084 (S-1084b) — drop people.name. The localized display name moved
-- to people_texts (000035); the language-neutral fallback is
-- people.original_name. All readers repointed + coverage-verified in 1084a.
-- Down re-adds it NULLABLE (backfill from people_texts en-US tier is a
-- separate manual op).
-- Pre-drop backfill (DF-4): preserve the last-writer base tier into
-- original_name where it is missing, so no person loses its terminal
-- fallback (avoids NULL display names for pre-1083 rows whose TMDB
-- original_name was never captured). Must run BEFORE the drop, while
-- `name` still exists.
UPDATE "people" SET "original_name" = "name" WHERE "original_name" IS NULL OR "original_name" = '';
-- modify "people" table
-- atlas:nolint destructive
ALTER TABLE "people" DROP COLUMN "name";
