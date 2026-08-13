-- modify "movies" table
ALTER TABLE "movies" ADD COLUMN "enrichment_text_synced_at" timestamptz NULL, ADD COLUMN "enrichment_cast_synced_at" timestamptz NULL, ADD COLUMN "enrichment_recs_synced_at" timestamptz NULL, ADD COLUMN "enrichment_media_synced_at" timestamptz NULL, ADD COLUMN "enrichment_keywords_synced_at" timestamptz NULL;
