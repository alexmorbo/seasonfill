package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

var _ ports.NotifiedEventsRepository = (*NotifiedEventsRepository)(nil)

// NotifiedEventsRepository persists the notified_events dedup ledger. Insert
// routes through the tx-scoped DB from ctx (dbtx) so MarkIfNew + the outbox
// Insert made by a producer are atomic with each other.
type NotifiedEventsRepository struct{ db *gorm.DB }

func NewNotifiedEventsRepository(db *gorm.DB) *NotifiedEventsRepository {
	return &NotifiedEventsRepository{db: db}
}

// MarkIfNew inserts the (event_type, entity_key) marker with ON CONFLICT DO
// NOTHING. RowsAffected>0 ⇒ a new marker was created (first observation);
// ==0 ⇒ the marker already existed (re-scan / Changes-storm). Works on both
// dialects: Postgres ON CONFLICT DO NOTHING and SQLite INSERT OR IGNORE both
// report 0 affected rows on conflict.
func (r *NotifiedEventsRepository) MarkIfNew(ctx context.Context, eventType, entityKey string, now time.Time) (bool, error) {
	if eventType == "" {
		return false, fmt.Errorf("mark notified_event: event_type must be non-empty")
	}
	if entityKey == "" {
		return false, fmt.Errorf("mark notified_event: entity_key must be non-empty")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	m := database.NotifiedEventModel{
		EventType:   eventType,
		EntityKey:   entityKey,
		FirstSeenAt: now.UTC(),
	}
	res := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "event_type"}, {Name: "entity_key"}},
		DoNothing: true,
	}).Create(&m)
	if res.Error != nil {
		return false, fmt.Errorf("mark notified_event: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}
