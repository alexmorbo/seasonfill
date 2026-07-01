package adapters

import (
	"context"
	"sync"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesTextRefresher is the narrow slice of the enrichment SeriesWorker
// the pre-warm adapter reads through. Kept local so unit tests can pass
// a hand-rolled fake without importing enrichment.
//
// The production binding is *appenrich.SeriesWorker.RefreshSeriesText —
// the enrichment worker's narrow A2 method (Story 559). It's probe-gated
// when force=false, so pre-warm calls on already-warm rows short-circuit
// without a TMDB fetch.
type SeriesTextRefresher interface {
	RefreshSeriesText(ctx context.Context, seriesID domain.SeriesID, lang string, force bool) error
}

// DiscoveryPreWarmerHolder satisfies discoapp.SeriesTextPreWarmer and
// forwards to a late-bound SeriesTextRefresher. Same holder pattern as
// OnDemandEnricherHolder — the enrichment SeriesWorker doesn't exist at
// BuildDiscoveryRuntime time (server.go's LATE BIND ZONE wires
// SeriesFreshenerHolder before BuildDiscoveryRuntime — however, we
// still route through a holder here because the adapter interface
// contract stays consistent with the wiring pattern used elsewhere and
// lets tests fake the inner worker easily).
//
// force is HARD-CODED to false — Story 568 A2 requires probe-gated
// pre-warm; force=true would double-write on every Tick and blow the
// TMDB budget.
//
// Nil-inner behavior: PreWarm becomes a no-op returning nil. This
// happens transiently during boot (Set() not yet called) and is
// acceptable because the discovery worker's first Tick fires after
// server.go completes its LATE BIND ZONE — the empty window is
// bounded and A2 will simply skip pre-warm on that first pass. The
// nil-return keeps the outcome accounting as "skip_no_tmdb_id" from
// the worker's perspective (nil == success == warmed), but the caller's
// summary log line will still emit and the next Tick will find the
// inner set.
type DiscoveryPreWarmerHolder struct {
	mu    sync.RWMutex
	inner SeriesTextRefresher
}

// Compile-time assertion: the holder satisfies the discovery port.
var _ discoapp.SeriesTextPreWarmer = (*DiscoveryPreWarmerHolder)(nil)

// NewDiscoveryPreWarmerHolder returns a holder with no inner refresher.
// Call Set once wireEnrichment has produced the SeriesWorker.
func NewDiscoveryPreWarmerHolder() *DiscoveryPreWarmerHolder {
	return &DiscoveryPreWarmerHolder{}
}

// Set wires the inner refresher. Idempotent.
func (h *DiscoveryPreWarmerHolder) Set(inner SeriesTextRefresher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inner = inner
}

// PreWarm satisfies discoapp.SeriesTextPreWarmer. Delegates to the
// inner refresher with force=false. Nil inner → no-op (returns nil so
// the worker classifies as "warmed" — see holder godoc).
func (h *DiscoveryPreWarmerHolder) PreWarm(ctx context.Context, seriesID domain.SeriesID, lang string) error {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return nil
	}
	return inner.RefreshSeriesText(ctx, seriesID, lang, false)
}

// NoOpDiscoveryPreWarmer is a nil-safe implementation of the
// SeriesTextPreWarmer port used when discoveryPreWarm.enabled=false.
// The worker's refresh() branches on `w.preWarmer != nil` — so the
// wiring layer explicitly passes NIL for the disabled path rather than
// this type — but the sentinel is exported for tests that want a
// non-nil no-op.
type NoOpDiscoveryPreWarmer struct{}

// Compile-time assertion.
var _ discoapp.SeriesTextPreWarmer = NoOpDiscoveryPreWarmer{}

// PreWarm returns nil unconditionally.
func (NoOpDiscoveryPreWarmer) PreWarm(context.Context, domain.SeriesID, string) error {
	return nil
}
