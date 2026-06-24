// refresh.go — Story 534 ticker driver for the background refresh
// scheduler. Mirrors discovery.go: wirer owns the scheduler
// construction; the loop owns ctx and boot/shutdown logs.
package loops

import (
	"context"
	"log/slog"
	"time"

	enrichapp "github.com/alexmorbo/seasonfill/internal/enrichment/app"
)

// DefaultRefreshInterval is the production cadence — 30 minutes per
// the Story 534 spec. interval <= 0 falls back to this so a misconfig
// can't accidentally tick-storm the worker.
const DefaultRefreshInterval = 30 * time.Minute

// RunRefresh blocks until ctx is cancelled. The scheduler's
// RunForever owns the immediate-first-tick + ticker.
func RunRefresh(ctx context.Context, s *enrichapp.RefreshScheduler, interval time.Duration, log *slog.Logger) {
	if s == nil {
		// Defensive — wirer skipped construction (cron disabled or
		// missing deps). Caller already lifecycle.Go-wrapped us; just
		// log and return so the goroutine drains.
		if log != nil {
			log.Info("refresh scheduler not configured; loop is a no-op")
		}
		return
	}
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	if log != nil {
		log.InfoContext(ctx, "refresh scheduler started",
			slog.Duration("interval", interval),
		)
	}
	s.RunForever(ctx, interval)
	if log != nil {
		log.InfoContext(ctx, "refresh scheduler stopped")
	}
}
