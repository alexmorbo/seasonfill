package app

import (
	"sync"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieHotEnqueuer is the late-bound holder for the S1b Hot-lane enqueuer.
// BuildMovieDetail constructs the UseCase (and this holder) BEFORE the
// enrichment dispatcher is reachable at the wiring seam (BuildMovieDetail runs
// inside BuildHTTPServer, which has no dispatcher arg), so the holder is
// injected empty via WithEnrichmentEnqueuer and Set(inner) is called from
// cmd/server's late-bind zone once the dispatcher exists. Until Set,
// EnqueueMovieHot is a safe no-op (the movie is still covered by the
// MarkStaleForReenrich background nudge). Mirrors the S1a MovieFreshener holder.
type MovieHotEnqueuer struct {
	mu    sync.RWMutex
	inner EnrichmentEnqueuer
}

// NewMovieHotEnqueuer constructs an unbound holder.
func NewMovieHotEnqueuer() *MovieHotEnqueuer { return &MovieHotEnqueuer{} }

// Set late-binds the real enqueuer. Idempotent; safe to call concurrently with
// EnqueueMovieHot (in practice Set runs at boot before the HTTP server serves).
func (h *MovieHotEnqueuer) Set(inner EnrichmentEnqueuer) {
	h.mu.Lock()
	h.inner = inner
	h.mu.Unlock()
}

// EnqueueMovieHot delegates to the bound enqueuer, or no-ops when unbound (the
// boot window before the dispatcher is late-bound). *MovieHotEnqueuer therefore
// satisfies EnrichmentEnqueuer and is safe to inject into the usecase eagerly.
func (h *MovieHotEnqueuer) EnqueueMovieHot(movieID domain.MovieID) {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return
	}
	inner.EnqueueMovieHot(movieID)
}
