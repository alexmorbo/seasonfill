-- 071: Add last_aired_at (Sonarr `previousAiring`) so the F11 series list
-- can sort by latest aired episode. Nullable — Sonarr omits the field
-- for upcoming series with no aired episodes yet. The list endpoint's
-- new `air_date_desc` sort key uses NULL-last ordering.
ALTER TABLE series_cache
    ADD COLUMN last_aired_at timestamp with time zone;
