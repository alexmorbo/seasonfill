package repositories

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// QbitTorrentEventsRepository persists the qbit_torrent_events
// table (migration 000035, PRD §7.3). Append-only — there is no
// update path. The weekly GC sweep from story 218 (E-2) prunes
// rows older than 180 days.
type QbitTorrentEventsRepository struct {
	db    *gorm.DB
	clock func() time.Time
}

// NewQbitTorrentEventsRepository constructs the repo bound to db.
func NewQbitTorrentEventsRepository(db *gorm.DB) *QbitTorrentEventsRepository {
	return &QbitTorrentEventsRepository{
		db:    db,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// Insert appends one event row. `from_group` is nil for non-
// state-change events; `to_group` is filled for every event
// except `deleted` (the new state at delete is undefined). The
// repo wraps the gorm error so callers can `errors.Is` it.
func (r *QbitTorrentEventsRepository) Insert(ctx context.Context, row torrentsync.EventRow) error {
	at := row.At
	if at.IsZero() {
		at = r.clock()
	}
	m := database.QbitTorrentEventModel{
		InstanceName: row.Instance,
		TorrentHash:  domain.QbitHash(row.Hash),
		Event:        string(row.Event),
		OccurredAt:   at,
	}
	if row.From != "" {
		v := string(row.From)
		m.FromGroup = &v
	}
	if row.To != "" && row.Event != torrentsync.EventDeleted {
		v := string(row.To)
		m.ToGroup = &v
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("insert qbit_torrent_event: %w", err)
	}
	return nil
}

// Compile-time port check.
var _ torrentsync.EventsRepo = (*QbitTorrentEventsRepository)(nil)
