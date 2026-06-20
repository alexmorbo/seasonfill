DROP INDEX IF EXISTS series_cache_series_id;
ALTER TABLE series_cache DROP COLUMN IF EXISTS series_id;

DROP TABLE IF EXISTS episode_states;
DROP TABLE IF EXISTS episode_texts;
DROP INDEX IF EXISTS episodes_air_date;
DROP INDEX IF EXISTS episodes_natural;
DROP TABLE IF EXISTS episodes;
DROP INDEX IF EXISTS seasons_natural;
DROP TABLE IF EXISTS seasons;
DROP TABLE IF EXISTS series_texts;
DROP INDEX IF EXISTS series_imdb_id;
DROP INDEX IF EXISTS series_tvdb_id;
DROP INDEX IF EXISTS series_tmdb_id;
DROP TABLE IF EXISTS series;
