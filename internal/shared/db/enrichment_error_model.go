package database

import "time"

// EnrichmentErrorModel persists the enrichment_errors table (D-1
// migration 000008, PRD §4.4). Polymorphic (entity_type, entity_id)
// natural key plus (source) — composite UNIQUE on the trio. Only
// failure rows live here; success is recorded as the canonical entity
// row's enrichment_*_synced_at column being non-NULL.
type EnrichmentErrorModel struct {
	ID            int64      `gorm:"primaryKey;autoIncrement;column:id"`
	EntityType    string     `gorm:"column:entity_type;type:text;not null"`
	EntityID      int64      `gorm:"column:entity_id;not null"`
	Source        string     `gorm:"column:source;type:text;not null"`
	LastError     string     `gorm:"column:last_error;type:text;not null"`
	Attempts      int        `gorm:"column:attempts;not null;default:1"`
	FirstSeenAt   time.Time  `gorm:"column:first_seen_at;not null"`
	LastSeenAt    time.Time  `gorm:"column:last_seen_at;not null"`
	NextAttemptAt *time.Time `gorm:"column:next_attempt_at"`
}

func (EnrichmentErrorModel) TableName() string { return "enrichment_errors" }
