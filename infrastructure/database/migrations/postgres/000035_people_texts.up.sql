-- Story 1083 — per-language person DISPLAY name side-table (see sqlite up for
-- the full rationale). Mirrors person_credits_texts (000029) with a bigint FK.
CREATE TABLE "people_texts" (
  "person_id" bigint NOT NULL,
  "language" text NOT NULL,
  "name" text NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("person_id", "language"),
  CONSTRAINT "people_texts_person_id_fkey" FOREIGN KEY ("person_id") REFERENCES "people" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
