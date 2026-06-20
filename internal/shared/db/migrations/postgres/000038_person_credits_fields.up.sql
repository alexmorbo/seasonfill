-- Story 307: expand person_credits with TMDB fields the mapper already
-- emits but the model was silently dropping at the write boundary.
--
-- department      — TMDB credit department ("Production", "Writing",
--                   "Directing", "Editorial", "Sound", "Camera", ...).
--                   Free-form string; widest observed value in TMDB's
--                   schema is ~30 chars; varchar(64) leaves headroom.
-- original_title  — TMDB original_name (TV) / original_title (movie)
--                   for the credited media. Same length as
--                   person_credits.title (text in v30); we use
--                   varchar(255) here for parity with the canon
--                   series.original_title column (story 203 B-1a).
-- tmdb_votes      — TMDB vote_count on the credited media. Integer
--                   counter; nullable when TMDB returns 0 (the mapper
--                   uses nonZeroIntPtr).
--
-- All three columns are NULLABLE with no default. Existing rows stay
-- as NULL; the next person enrichment re-ingest (idempotent
-- BatchUpsert) populates them. No backfill SQL needed — TMDB's
-- /person/{id} endpoint is the source of truth, the C-3 person
-- worker re-runs it on the worker schedule, and the data flows in
-- naturally.

ALTER TABLE person_credits ADD COLUMN department     varchar(64);
ALTER TABLE person_credits ADD COLUMN original_title varchar(255);
ALTER TABLE person_credits ADD COLUMN tmdb_votes     integer;
