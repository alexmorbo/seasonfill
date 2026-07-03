-- reverse: modify "series" table (title re-added NULLABLE — a NOT NULL
-- re-add fails rollback on a populated table; backfill from series_texts
-- en-US is a separate manual op).
ALTER TABLE "series" ADD COLUMN "backdrop_asset" text NULL, ADD COLUMN "poster_asset" text NULL, ADD COLUMN "title" text NULL;
-- reverse: modify "seasons" table
ALTER TABLE "seasons" ADD COLUMN "poster_asset" text NULL, ADD COLUMN "overview" text NULL, ADD COLUMN "name" text NULL;
