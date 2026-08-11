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

// movieChangesStateRowID is the fixed primary key of the single-row
// movie_changes_state table (CHECK id = 1, migration 000054). Structural mirror
// of changesStateRowID — a DEDICATED cursor row so the /movie/changes walk never
// collides with the /tv/changes cursor (tmdb_changes_state).
const movieChangesStateRowID int64 = 1

// MovieChangesStateRepository round-trips the movie firehose ChangeCursor
// to/from the single-row movie_changes_state table. Byte-for-byte structural
// mirror of TMDBChangesStateRepository; implements the same app-layer
// enrichment.ChangesCursorStore port so the generic ChangesPoller consumes it
// with zero TV edits.
type MovieChangesStateRepository struct {
	db *gorm.DB
}

func NewMovieChangesStateRepository(db *gorm.DB) *MovieChangesStateRepository {
	return &MovieChangesStateRepository{db: db}
}

// Get returns the persisted cursor. ports.ErrNotFound (→ empty ChangeCursor)
// when the row has never been written (first run / cold start).
func (r *MovieChangesStateRepository) Get(ctx context.Context) (enrichmentpkg.ChangeCursor, error) {
	var m database.MovieChangesStateModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", movieChangesStateRowID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return enrichmentpkg.ChangeCursor{}, ports.ErrNotFound
		}
		return enrichmentpkg.ChangeCursor{}, fmt.Errorf("get movie changes state: %w", err)
	}
	return movieToChangeCursor(m), nil
}

// Save upserts the single (id=1) cursor row. OnConflict(id) updates the mutable
// columns + updated_at; created_at is set on first insert and preserved on
// update. SchemaVersion defaults to 1 when zero.
func (r *MovieChangesStateRepository) Save(ctx context.Context, c enrichmentpkg.ChangeCursor) error {
	now := time.Now().UTC()
	schemaVersion := c.SchemaVersion
	if schemaVersion == 0 {
		schemaVersion = 1
	}
	windowEnd := nullableTime(c.LastWindowEnd)
	pollAt := nullableTime(c.LastPollAt)

	m := database.MovieChangesStateModel{
		ID:            movieChangesStateRowID,
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
		return fmt.Errorf("save movie changes state: %w", err)
	}
	return nil
}

// movieToChangeCursor maps the model to the domain VO. Nullable timestamps map
// to the zero time (which PlanWindows reads as "empty cursor").
func movieToChangeCursor(m database.MovieChangesStateModel) enrichmentpkg.ChangeCursor {
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
