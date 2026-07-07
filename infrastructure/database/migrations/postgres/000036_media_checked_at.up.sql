-- Story 1081a — per-locale poster/backdrop PRESENCE markers on
-- series_media_texts (see sqlite up for the full rationale). Nullable
-- timestamptz; NULL = never checked. Stamped PLAIN excluded (not COALESCE) by
-- the TMDB media writers so a re-check refreshes the stamp; a confirmed-absent
-- row (asset NULL + checked_at SET) lets the hero serve the stable original
-- poster instead of the localized one that would otherwise swap in on poll.
ALTER TABLE "series_media_texts" ADD COLUMN "poster_checked_at" timestamptz NULL;
ALTER TABLE "series_media_texts" ADD COLUMN "backdrop_checked_at" timestamptz NULL;
