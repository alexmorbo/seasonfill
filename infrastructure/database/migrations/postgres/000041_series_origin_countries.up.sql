-- Story 365a: origin_countries — JSON-encoded array of ISO 3166-1
-- alpha-2 codes (e.g. '["US","CA"]'). Replaces the singular
-- series.origin_country for the read path (right-rail "Страны" row);
-- origin_country is kept in sync as origin_countries[0] for compat with
-- legacy callers (sonarr_sync, cold canon rows that never went through
-- TMDB).
ALTER TABLE series ADD COLUMN origin_countries text;
