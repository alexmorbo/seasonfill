-- reverse: create index "enrichment_errors_next_attempt" to table: "enrichment_errors"
DROP INDEX `enrichment_errors_next_attempt`;
-- reverse: create index "enrichment_errors_entity_source" to table: "enrichment_errors"
DROP INDEX `enrichment_errors_entity_source`;
-- reverse: create "enrichment_errors" table
DROP TABLE `enrichment_errors`;
