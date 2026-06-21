// Package enrichment — Story 212 cold-start backfill +
// Story 318 periodic re-sweep.
//
// BackfillSeries scans `series` rows whose enrichment_tmdb_synced_at IS
// NULL and enqueues them at PriorityCold. Person backfill happens
// organically — every successful series enrichment upserts person
// stubs and enqueues them (series_worker integration).
//
// Idempotency: after the first successful enrichment pass every series'
// enrichment_tmdb_synced_at is non-NULL. Subsequent passes' WHERE filter
// returns zero (or only newly-added) rows. The function is therefore
// safe to call from a recovery script, repeated restarts, OR a
// periodic ticker (Story 318 — the production path now calls
// BackfillSeries every 60s for the lifetime of the process, picking
// up rows the dispatcher dropped on a saturated cold channel during
// the previous sweep).
//
// 306: publishes the `enrichment_cold_start_remaining` gauge —
// initialised to len(ids) before the first enqueue, decremented to
// zero as each enqueued series completes. The decrement plumbing
// lives in the dispatcher's OnSeriesComplete hook (set on every
// call to BackfillSeries; nil-OK on the test path that uses a
// recordingDispatcher fake). Re-sweep semantics: each invocation
// overwrites the prior hook + re-publishes the gauge to the new
// len(ids). Previous-sweep completions that race the new
// SetOnSeriesComplete are dropped silently — the new gauge value
// reflects "remaining as of the latest scan", which is the operator
// signal we want.
//
// Story 318: BackfillSeries also publishes
// `enrichment_cold_start_resweeps_total` (always) and
// `enrichment_cold_start_resweep_enqueued_total` (when len(ids) > 0).
// RunBackfillLoop is the production goroutine entry point — runs
// once synchronously, then on a ticker until ctx cancels.

package enrichment

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/observability"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// coldStartGaugeSetter is the test seam for the gauge publisher. The
// production path binds to observability.SetEnrichmentColdStartRemaining;
// tests override via SetColdStartGaugeForTest.
var coldStartGaugeSetter atomic.Pointer[func(int)]

// SetColdStartGaugeForTest swaps the gauge publisher. Pass nil to
// restore the default. Test-only.
func SetColdStartGaugeForTest(fn func(int)) {
	if fn == nil {
		coldStartGaugeSetter.Store(nil)
		return
	}
	coldStartGaugeSetter.Store(&fn)
}

func setColdStartGauge(n int) {
	if p := coldStartGaugeSetter.Load(); p != nil {
		(*p)(n)
		return
	}
	observability.SetEnrichmentColdStartRemaining(n)
}

// hookableDispatcher is the subset of *DispatcherImpl that
// BackfillSeries needs to register a per-completion hook. The
// production *DispatcherImpl satisfies it directly; tests that pass
// a Dispatcher-only fake skip the hook (gauge decrement is a no-op
// for those callers, which is the documented behaviour — see story).
type hookableDispatcher interface {
	Dispatcher
	SetOnSeriesComplete(fn func(id int64))
}

// BackfillSeries enqueues every series_id returned by scanner at
// PriorityCold. The dispatcher's dedup protects against double-enqueue
// if the same id is already pending. Limit caps a single sweep at
// 5000 ids — enough to cover a typical library (~300) by an order
// of magnitude, bounded so a runaway query doesn't pin memory.
// Safe to call repeatedly (Story 318): each invocation ticks
// `enrichment_cold_start_resweeps_total` and overwrites the prior
// completion hook. Channel-full drops self-heal on the next call.
func BackfillSeries(ctx context.Context, scanner ColdStartScanner, dispatcher Dispatcher, log *slog.Logger) error {
	const sweepLimit = 5000
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	observability.IncEnrichmentColdStartResweep()
	ids, err := scanner.ListMissingTMDBSync(ctx, sweepLimit)
	if err != nil {
		return fmt.Errorf("cold-start scan: %w", err)
	}
	if len(ids) == 0 {
		log.InfoContext(ctx, "enrichment.cold_start.empty")
		setColdStartGauge(0)
		return nil
	}

	// 306 — build the owned-id set BEFORE the gauge is published so a
	// completion racing the enqueue loop sees a consistent set.
	owned := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		owned[int64(id)] = struct{}{}
	}
	var (
		mu        sync.Mutex
		remaining = len(owned)
	)
	setColdStartGauge(remaining)

	// Register the completion hook only on dispatchers that support it.
	// Production *DispatcherImpl satisfies hookableDispatcher; test
	// fakes that don't care (recordingDispatcher) skip the hook —
	// the gauge then never decrements, but the test path doesn't
	// assert on the gauge so this is acceptable.
	if hd, ok := dispatcher.(hookableDispatcher); ok {
		hd.SetOnSeriesComplete(func(id int64) {
			mu.Lock()
			defer mu.Unlock()
			if _, ours := owned[id]; !ours {
				return
			}
			delete(owned, id)
			remaining--
			if remaining < 0 {
				remaining = 0
			}
			setColdStartGauge(remaining)
		})
	}

	for _, id := range ids {
		dispatcher.Enqueue(EntitySeries, int64(id), PriorityCold)
	}
	observability.AddEnrichmentColdStartResweepEnqueued(len(ids))
	log.InfoContext(ctx, "enrichment.cold_start.enqueued",
		slog.Int("series_count", len(ids)),
		slog.String("priority", "cold"),
	)
	return nil
}

// RunBackfillLoop is the production driver for the cold-start re-sweep
// (Story 318). It runs BackfillSeries once synchronously, then on a
// ticker until ctx cancels. interval <= 0 collapses to a 60s default;
// the floor matches internal/config.coldStartResweepIntervalFromEnv,
// so a misconfiguration cannot turn this into a DB hot loop.
//
// Errors from BackfillSeries are logged at WARN and do NOT terminate
// the loop — a transient DB blip on one tick must not silence the
// re-sweep forever.
//
// The function returns when ctx is Done. main.go wraps the call in
// bgWG so shutdown waits for the goroutine to exit cleanly.
func RunBackfillLoop(ctx context.Context, scanner ColdStartScanner, dispatcher Dispatcher, interval time.Duration, log *slog.Logger) {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if interval <= 0 {
		interval = 60 * time.Second
	}
	// Initial synchronous sweep keeps boot-time behaviour identical
	// to the pre-Story-318 codepath.
	if err := BackfillSeries(ctx, scanner, dispatcher, log); err != nil {
		log.WarnContext(ctx, "enrichment.cold_start.failed",
			slog.String("error", err.Error()))
	}
	if err := ctx.Err(); err != nil {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	log.InfoContext(ctx, "enrichment.cold_start.resweep.started",
		slog.Duration("interval", interval))
	for {
		select {
		case <-ctx.Done():
			log.InfoContext(ctx, "enrichment.cold_start.resweep.stopped")
			return
		case <-ticker.C:
			if err := BackfillSeries(ctx, scanner, dispatcher, log); err != nil {
				log.WarnContext(ctx, "enrichment.cold_start.failed",
					slog.String("error", err.Error()))
			}
		}
	}
}
