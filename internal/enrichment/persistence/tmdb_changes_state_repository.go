package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	enrichmentpkg "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// changesStateRowID is the fixed primary key of the single-row tmdb_changes_state
// table (CHECK id = 1, migration 000041 / plan §5.2). The store always reads and
// writes this row — the cursor is process-global machine state, not per-entity.
const changesStateRowID int64 = 1

// TMDBChangesStateRepository round-trips the firehose ChangeCursor to/from the
// single-row tmdb_changes_state table. Machine state, sibling of the watchdog /
// quota state repos — NOT operator-editable app_config (plan §5.2). Implements
// the app-layer enrichment.ChangesCursorStore port.
type TMDBChangesStateRepository struct {
	db *gorm.DB
}

func NewTMDBChangesStateRepository(db *gorm.DB) *TMDBChangesStateRepository {
	return &TMDBChangesStateRepository{db: db}
}

// Get returns the persisted cursor. ports.ErrNotFound (→ empty ChangeCursor) when
// the row has never been written (first run / cold start, plan §4.5).
func (r *TMDBChangesStateRepository) Get(ctx context.Context) (enrichmentpkg.ChangeCursor, error) {
	var m database.TMDBChangesStateModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", changesStateRowID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return enrichmentpkg.ChangeCursor{}, ports.ErrNotFound
		}
		return enrichmentpkg.ChangeCursor{}, fmt.Errorf("get tmdb changes state: %w", err)
	}
	return toChangeCursor(m), nil
}

// Save upserts the single (id=1) cursor row. OnConflict(id) updates the mutable
// columns + updated_at; created_at is set on first insert and preserved on
// update (excluded from DoUpdates). SchemaVersion defaults to 1 when zero so a
// domain cursor built without it round-trips to the migration default.
func (r *TMDBChangesStateRepository) Save(ctx context.Context, c enrichmentpkg.ChangeCursor) error {
	now := time.Now().UTC()
	schemaVersion := c.SchemaVersion
	if schemaVersion == 0 {
		schemaVersion = 1
	}
	windowEnd := nullableTime(c.LastWindowEnd)
	pollAt := nullableTime(c.LastPollAt)

	m := database.TMDBChangesStateModel{
		ID:            changesStateRowID,
		SchemaVersion: schemaVersion,
		LastWindowEnd: windowEnd,
		LastPollAt:    pollAt,
		LastMatched:   c.LastMatched,
		LastFirehose:  c.LastFirehose,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"schema_version":  schemaVersion,
			"last_window_end": windowEnd,
			"last_poll_at":    pollAt,
			"last_matched":    c.LastMatched,
			"last_firehose":   c.LastFirehose,
			"updated_at":      now,
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("save tmdb changes state: %w", err)
	}
	return nil
}

// toChangeCursor maps the model to the domain VO. Nullable timestamps map to the
// zero time (which PlanWindows reads as "empty cursor").
func toChangeCursor(m database.TMDBChangesStateModel) enrichmentpkg.ChangeCursor {
	c := enrichmentpkg.ChangeCursor{
		SchemaVersion: m.SchemaVersion,
		LastMatched:   m.LastMatched,
		LastFirehose:  m.LastFirehose,
	}
	if m.LastWindowEnd != nil {
		c.LastWindowEnd = m.LastWindowEnd.UTC()
	}
	if m.LastPollAt != nil {
		c.LastPollAt = m.LastPollAt.UTC()
	}
	return c
}

// nullableTime maps the zero time to a nil pointer (SQL NULL) and any other value
// to its UTC pointer.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}
