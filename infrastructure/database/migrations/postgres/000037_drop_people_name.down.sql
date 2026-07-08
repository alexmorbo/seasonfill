-- reverse: re-add people.name NULLABLE (a NOT NULL re-add fails rollback
-- on a populated table; backfill from people_texts en-US is a separate
-- manual op — matches the 000028 precedent).
ALTER TABLE "people" ADD COLUMN "name" text NULL;
