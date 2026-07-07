-- Story 1083 — per-language person DISPLAY name side-table. people.name is a
-- single last-writer-wins column (a ru-RU RefreshCast pass stamps Cyrillic,
-- then an en-US reader reads it back). This table keys the TMDB-localized
-- aggregate_credits[*].name by (person_id, language); the cast read-path
-- resolves requested-lang -> en-US -> people.original_name -> people.name.
-- Mirrors person_credits_texts (000029). FK CASCADE so a person delete reaps
-- its localized names.
CREATE TABLE `people_texts` (
  `person_id` integer NOT NULL,
  `language` text NOT NULL,
  `name` text NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`person_id`, `language`),
  CONSTRAINT `people_texts_person_id_fkey` FOREIGN KEY (`person_id`) REFERENCES `people` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
