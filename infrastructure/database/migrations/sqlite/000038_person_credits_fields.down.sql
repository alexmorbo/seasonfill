-- Plain DROP COLUMN works on sqlite >= 3.35 (2021-03). The pure-Go
-- glebarez/sqlite driver bundles a current sqlite. Same pattern as
-- 000003_oidc.down.sql which drops 7 columns this way.
ALTER TABLE person_credits DROP COLUMN tmdb_votes;
ALTER TABLE person_credits DROP COLUMN original_title;
ALTER TABLE person_credits DROP COLUMN department;
