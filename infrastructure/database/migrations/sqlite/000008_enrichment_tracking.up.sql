-- create "enrichment_errors" table
CREATE TABLE `enrichment_errors` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `entity_type` text NOT NULL,
  `entity_id` integer NOT NULL,
  `source` text NOT NULL,
  `last_error` text NOT NULL,
  `attempts` integer NOT NULL DEFAULT 1,
  `first_seen_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `last_seen_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  `next_attempt_at` datetime NULL
);
-- create index "enrichment_errors_entity_source" to table: "enrichment_errors"
CREATE UNIQUE INDEX `enrichment_errors_entity_source` ON `enrichment_errors` (`entity_type`, `entity_id`, `source`);
-- create index "enrichment_errors_next_attempt" to table: "enrichment_errors"
CREATE INDEX `enrichment_errors_next_attempt` ON `enrichment_errors` (`next_attempt_at`) WHERE next_attempt_at IS NOT NULL;
