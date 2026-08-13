-- reverse: modify "movies" table
ALTER TABLE "movies" DROP COLUMN "enrichment_keywords_synced_at", DROP COLUMN "enrichment_media_synced_at", DROP COLUMN "enrichment_recs_synced_at", DROP COLUMN "enrichment_cast_synced_at", DROP COLUMN "enrichment_text_synced_at";
