// Package enrichment — Story 212 cold-start backfill.
//
// BackfillSeries scans `series` rows that lack a sync_log(tmdb_series)
// row and enqueues them at PriorityCold. Person backfill happens
// organically — every successful series enrichment upserts person
// stubs and enqueues them (series_worker integration).
//
// Idempotency: after the first pass every series acquires a sync_log
// row (outcome ∈ {ok, error, not_found}). The second pass's LEFT JOIN
// returns zero rows. The function is therefore safe to call from a
// recovery script or repeated restarts.
//
// 306: publishes the `enrichment_cold_start_remaining` gauge —
// initialised to len(ids) before the first enqueue, decremented to
// zero as each enqueued series completes. The decrement plumbing
// lives in the dispatcher's OnSeriesComplete hook (set on every
// call to BackfillSeries; nil-OK on the test path that uses a
// recordingDispatcher fake).

package enrichment

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/alexmorbo/seasonfill/internal/observability"
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
func BackfillSeries(ctx context.Context, scanner ColdStartScanner, dispatcher Dispatcher, log *slog.Logger) error {
	const sweepLimit = 5000
	if log == nil {
		log = slog.Default()
	}
	ids, err := scanner.ListMissingSyncLog(ctx, "tmdb_series", sweepLimit)
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
		owned[id] = struct{}{}
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
		dispatcher.Enqueue(EntitySeries, id, PriorityCold)
	}
	log.InfoContext(ctx, "enrichment.cold_start.enqueued",
		slog.Int("series_count", len(ids)),
		slog.String("priority", "cold"),
	)
	return nil
}
