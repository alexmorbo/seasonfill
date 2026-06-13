// event_prune.go — story 218 E-2.
//
// Prunes qbit_torrent_events older than 180d (PRD §6.7). The table
// is added by the A-* branch (story 219+); the existence probe lets
// 218 ship before 219 lands and the prune become a no-op skip in
// that window. As of 219 the schema uses `occurred_at` as the row
// timestamp (PRD §7.3).

package gc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

// EventPruneDeps is the consumer-side bundle. DB is the raw gorm
// handle — we run two raw statements (existence probe + delete) and
// don't need a full repository surface.
type EventPruneDeps struct {
	DB     *gorm.DB
	Clock  func() time.Time
	Logger *slog.Logger
	// RetentionAge overrides the default 180d.
	RetentionAge time.Duration
}

// Build constructs the WeeklyJob.EventPrune closure.
func (d EventPruneDeps) Build() func(ctx context.Context) (EventPruneResult, error) {
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	retention := d.RetentionAge
	if retention == 0 {
		retention = 180 * 24 * time.Hour
	}
	return func(ctx context.Context) (EventPruneResult, error) {
		if !tableExists(ctx, d.DB, "qbit_torrent_events") {
			return EventPruneResult{
				Skipped:    true,
				SkipReason: "table_not_present_pending_a3",
			}, nil
		}
		cutoff := clock().Add(-retention)
		res := d.DB.WithContext(ctx).
			Exec(`DELETE FROM qbit_torrent_events WHERE occurred_at < ?`, cutoff)
		if res.Error != nil {
			return EventPruneResult{}, fmt.Errorf("prune qbit_torrent_events: %w", res.Error)
		}
		log.InfoContext(ctx, "event_prune.deleted",
			slog.Int64("rows", res.RowsAffected),
			slog.Time("cutoff", cutoff))
		return EventPruneResult{Deleted: int(res.RowsAffected)}, nil
	}
}

// tableExists probes information_schema (postgres) / sqlite_master
// (sqlite). Returns false on any error — skip silently rather than
// crash the GC.
func tableExists(ctx context.Context, db *gorm.DB, name string) bool {
	if db == nil {
		return false
	}
	dialect := db.Name()
	var probe string
	switch dialect {
	case "postgres":
		probe = `SELECT 1 FROM information_schema.tables WHERE table_name = ? LIMIT 1`
	case "sqlite":
		probe = `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1`
	default:
		return false
	}
	var found int
	err := db.WithContext(ctx).Raw(probe, name).Scan(&found).Error
	return err == nil && found == 1
}
