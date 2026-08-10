-- create "notified_events" table
CREATE TABLE `notified_events` (
  `event_type` text NOT NULL,
  `entity_key` text NOT NULL,
  `first_seen_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`event_type`, `entity_key`)
);
