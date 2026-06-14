-- Story 307: sqlite mirror of 000038. Type mapping:
--   * varchar(64)       → text
--   * varchar(255)      → text
--   * integer           → integer

ALTER TABLE person_credits ADD COLUMN department     text;
ALTER TABLE person_credits ADD COLUMN original_title text;
ALTER TABLE person_credits ADD COLUMN tmdb_votes     integer;
