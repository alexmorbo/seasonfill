-- reverse: add poster_checked_at / backdrop_checked_at to series_media_texts
ALTER TABLE `series_media_texts` DROP COLUMN `backdrop_checked_at`;
ALTER TABLE `series_media_texts` DROP COLUMN `poster_checked_at`;
