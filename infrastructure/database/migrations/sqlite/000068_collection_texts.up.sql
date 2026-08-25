-- create "collection_texts" table
CREATE TABLE `collection_texts` (
  `collection_id` integer NOT NULL,
  `language` text NOT NULL,
  `name` text NULL,
  `overview` text NULL,
  `enriched_at` datetime NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`collection_id`, `language`),
  CONSTRAINT `collection_texts_collection_id_fkey` FOREIGN KEY (`collection_id`) REFERENCES `collections` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
