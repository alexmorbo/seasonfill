package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
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
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("insert qbit_torrent_event: %w", err)
	}
	return nil
}

// PruneOlderThan deletes qbit_torrent_events rows with occurred_at < cutoff.
// Returns (deleted, skipped=false, "", nil) on the happy path. When the
// table does not yet exist (pre-A-1 schemas), returns
// (0, true, "table_not_present_pending_a3", nil) so the caller logs a
// skip rather than treating it as a failure.
//
// Story 421 (A-3 mini) lifted this out of application/gc/event_prune.go
// so the application layer no longer imports gorm.io/gorm.
func (r *QbitTorrentEventsRepository) PruneOlderThan(ctx context.Context, cutoff time.Time) (int, bool, string, error) {
	if !r.tableExists(ctx, "qbit_torrent_events") {
		return 0, true, "table_not_present_pending_a3", nil
	}
	res := r.db.WithContext(ctx).
		Exec(`DELETE FROM qbit_torrent_events WHERE occurred_at < ?`, cutoff)
	if res.Error != nil {
		return 0, false, "", fmt.Errorf("prune qbit_torrent_events: %w", res.Error)
	}
	return int(res.RowsAffected), false, "", nil
}

// tableExists probes information_schema (postgres) / sqlite_master (sqlite).
// Returns false on any error so callers can skip silently rather than crash.
// Moved from application/gc/event_prune.go in story 421.
func (r *QbitTorrentEventsRepository) tableExists(ctx context.Context, name string) bool {
	if r.db == nil {
		return false
	}
	var probe string
	switch r.db.Name() {
	case "postgres":
		probe = `SELECT 1 FROM information_schema.tables WHERE table_name = ? LIMIT 1`
	case "sqlite":
		probe = `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1`
	default:
		return false
	}
	var found int
	err := r.db.WithContext(ctx).Raw(probe, name).Scan(&found).Error
	return err == nil && found == 1
}

// Compile-time port check.
var _ torrentsync.EventsRepo = (*QbitTorrentEventsRepository)(nil)

// Compile-time port check for the prune surface (story 421).
var _ torrentsync.EventsPruner = (*QbitTorrentEventsRepository)(nil)
