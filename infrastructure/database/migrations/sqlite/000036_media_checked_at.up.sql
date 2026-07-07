-- Story 1081a — per-locale poster/backdrop PRESENCE markers on
-- series_media_texts. Distinguishes "checked TMDB, no localized art exists"
-- (asset NULL + checked_at SET = confirmed-absent → reader serves the stable
-- original/canonical poster) from "never checked" (asset NULL + checked_at
-- NULL → cold ModeSync resolves presence first). Nullable; NULL = never
-- checked. Written as PLAIN excluded (not COALESCE) so a re-check refreshes
-- the stamp. No backfill: every existing row is treated as never-checked
-- until the next TMDB media refresh stamps it.
ALTER TABLE `series_media_texts` ADD COLUMN `poster_checked_at` datetime NULL;
ALTER TABLE `series_media_texts` ADD COLUMN `backdrop_checked_at` datetime NULL;
