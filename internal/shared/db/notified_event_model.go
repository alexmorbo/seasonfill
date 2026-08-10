package database

import "time"

// NotifiedEventModel persists the notified_events table (ADR-0016 Ф4 N3,
// migration 000049). Cross-time dedup ledger for the calendar-event
// producers: the notification dispatcher DELETES outbox rows on successful
// send, so the outbox alone cannot dedup a signal across daily/weekly scans.
// A producer does INSERT … ON CONFLICT DO NOTHING here and enqueues the
// outbox row ONLY when a new marker row was created. Composite PK
// (event_type, entity_key); no FK — the ledger outlives any series row and
// is swept lazily by re-derivation, not by cascade.
type NotifiedEventModel struct {
	EventType   string    `gorm:"primaryKey;column:event_type;type:text"`
	EntityKey   string    `gorm:"primaryKey;column:entity_key;type:text"`
	FirstSeenAt time.Time `gorm:"column:first_seen_at;not null"`
}

func (NotifiedEventModel) TableName() string { return "notified_events" }
