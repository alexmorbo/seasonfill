// event_prune.go — story 218 E-2, refactored in story 421 (A-3 mini).
//
// Prunes qbit_torrent_events older than 180d (PRD §6.7). The
// existence-probe + DELETE pair now lives in the qbit-torrent-events
// repository so the application layer no longer depends on the ORM.

package gc

import (
	"context"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/application/torrentsync"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// EventPruneDeps is the consumer-side bundle. Repo is the
// torrentsync.EventsPruner port; the GC owns only the cadence,
// retention window, and result-shape mapping.
type EventPruneDeps struct {
	Repo   torrentsync.EventsPruner
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
		log = sharedports.DomainLogger(slog.Default(), "gc")
	}
	retention := d.RetentionAge
	if retention == 0 {
		retention = 180 * 24 * time.Hour
	}
	return func(ctx context.Context) (EventPruneResult, error) {
		if d.Repo == nil {
			return EventPruneResult{
				Skipped:    true,
				SkipReason: "repo_not_configured",
			}, nil
		}
		cutoff := clock().Add(-retention)
		deleted, skipped, skipReason, err := d.Repo.PruneOlderThan(ctx, cutoff)
		if err != nil {
			return EventPruneResult{}, err
		}
		if skipped {
			return EventPruneResult{
				Skipped:    true,
				SkipReason: skipReason,
			}, nil
		}
		log.InfoContext(ctx, "event_prune.deleted",
			slog.Int("rows", deleted),
			slog.Time("cutoff", cutoff))
		return EventPruneResult{Deleted: deleted}, nil
	}
}
