-- create "person_credits_texts" table
CREATE TABLE `person_credits_texts` (
  `person_credit_id` integer NOT NULL,
  `language` text NOT NULL,
  `character_name` text NULL,
  `updated_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`person_credit_id`, `language`),
  CONSTRAINT `person_credits_texts_person_credit_id_fkey` FOREIGN KEY (`person_credit_id`) REFERENCES `person_credits` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
