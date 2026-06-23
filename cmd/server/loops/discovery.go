// discovery.go is the goroutine entry point for the DiscoveryWorker.
// Story 506. Pattern matches sweep.go / qbit_capacity.go — the
// loop owns ctx cancellation, the wirer owns the worker construction.
//
// Production wires this via lifecycle.Go("discovery-worker", ...) so
// graceful shutdown drains the goroutine before the process exits.
package loops

import (
	"context"
	"log/slog"
	"time"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
)

// DefaultDiscoveryInterval is the production tick cadence (PRD §5.1.1
// line 627). 1h is "fast enough for a fresh-pod cold-start to populate
// every list within minutes, slow enough that TMDB rate-limits never
// surface in normal operation".
const DefaultDiscoveryInterval = time.Hour

// RunDiscovery blocks until ctx is cancelled. interval <= 0 falls
// back to DefaultDiscoveryInterval so a misconfigured runtime
// settings.toml can't accidentally tick-storm the worker.
//
// The worker's RunForever itself fires the first Tick IMMEDIATELY
// (cold-start contract per PRD §5.1.1 line 666) — RunDiscovery is
// a thin wrapper that adds boot/shutdown logging at the loop
// boundary so operators can correlate worker lifecycle with pod
// lifecycle in kubectl logs.
func RunDiscovery(ctx context.Context, w *discoapp.Worker, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultDiscoveryInterval
	}
	if log != nil {
		log.InfoContext(ctx, "discovery worker started",
			slog.Duration("interval", interval))
	}
	w.RunForever(ctx, interval)
	if log != nil {
		log.InfoContext(ctx, "discovery worker stopped")
	}
}
