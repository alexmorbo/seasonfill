-- ADR-0024 S1.1 — universal-search DB foundation. Adds the pg_trgm/unaccent
-- machinery and the expression-GIN indexes that back local-first search.
--
-- pg_trgm + unaccent are contrib extensions (present in the prod PG16 image
-- and the atlas/testcontainer PG17 dev image — F-06). f_unaccent wraps the
-- 2-arg unaccent(regdictionary,text) form, which is IMMUTABLE, so the naked
-- STABLE unaccent() cannot be used in an index expression (F-02). The
-- dictionary and function are schema-qualified (public.) and the dictionary
-- name is cast ::regdictionary so the wrapper stays resolvable while it is
-- inlined into the CREATE INDEX expressions below — an unqualified/uncast
-- 'unaccent' literal fails to coerce during index inlining (F-02b).
--
-- This prelude (the two CREATE EXTENSION + the CREATE OR REPLACE FUNCTION)
-- is byte-identical to cmd/loader/main.go:pgSearchPrelude so Atlas's dev DB
-- and the runtime DB agree. Atlas does not introspect extensions/functions,
-- so they are invisible to the drift gate on both sides (S1.0 verdict).
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE OR REPLACE FUNCTION f_unaccent(text) RETURNS text
  LANGUAGE sql IMMUTABLE PARALLEL SAFE
  AS $$ SELECT public.unaccent('public.unaccent'::regdictionary, $1) $$;
-- Each query predicate MUST call the identical lower(f_unaccent(col)) or the
-- planner will not use these indexes. NOTE: CREATE INDEX (non-CONCURRENTLY —
-- golang-migrate wraps the file in a single transaction) briefly write-locks
-- each table during the build; on people (~650K rows) this is a short lock,
-- acceptable at homelab scale.
CREATE INDEX IF NOT EXISTS "series_texts_title_trgm_idx" ON "series_texts" USING gin (lower(f_unaccent(title)) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS "movie_i18n_title_trgm_idx" ON "movie_i18n" USING gin (lower(f_unaccent(title)) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS "movies_original_title_trgm_idx" ON "movies" USING gin (lower(f_unaccent(original_title)) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS "movies_title_trgm_idx" ON "movies" USING gin (lower(f_unaccent(title)) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS "people_original_name_trgm_idx" ON "people" USING gin (lower(f_unaccent(original_name)) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS "people_texts_name_trgm_idx" ON "people_texts" USING gin (lower(f_unaccent(name)) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS "collections_name_trgm_idx" ON "collections" USING gin (lower(f_unaccent(name)) gin_trgm_ops);
