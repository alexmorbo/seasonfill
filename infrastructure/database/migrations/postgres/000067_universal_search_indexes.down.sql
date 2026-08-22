-- Reverse of 000067. Drop the 7 search indexes and the f_unaccent wrapper.
-- The pg_trgm and unaccent extensions are deliberately LEFT IN PLACE: other
-- objects may come to depend on them, and dropping a contrib extension is a
-- broad, cluster-affecting operation that a schema-version rollback should
-- not trigger. Re-running the up is idempotent (IF NOT EXISTS).
DROP INDEX IF EXISTS "collections_name_trgm_idx";
DROP INDEX IF EXISTS "people_texts_name_trgm_idx";
DROP INDEX IF EXISTS "people_original_name_trgm_idx";
DROP INDEX IF EXISTS "movies_title_trgm_idx";
DROP INDEX IF EXISTS "movies_original_title_trgm_idx";
DROP INDEX IF EXISTS "movie_i18n_title_trgm_idx";
DROP INDEX IF EXISTS "series_texts_title_trgm_idx";
DROP FUNCTION IF EXISTS f_unaccent(text);
