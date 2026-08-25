-- create "collection_texts" table
CREATE TABLE "collection_texts" (
  "collection_id" bigint NOT NULL,
  "language" text NOT NULL,
  "name" text NULL,
  "overview" text NULL,
  "enriched_at" timestamptz NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("collection_id", "language"),
  CONSTRAINT "collection_texts_collection_id_fkey" FOREIGN KEY ("collection_id") REFERENCES "collections" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "collection_texts_name_trgm_idx" to table: "collection_texts"
CREATE INDEX "collection_texts_name_trgm_idx" ON "collection_texts" USING gin ((lower(f_unaccent(name))) gin_trgm_ops);
