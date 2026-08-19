package app

import (
	"context"
	"sync"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieStubResolverHolder is the late-bound holder for the S2 stub-create-on-view
// resolver. BuildMovieDetail constructs the UseCase (and this holder) BEFORE the
// runtime TMDB client holder + discovery seed path are reachable at the wiring
// seam (BuildMovieDetail runs inside BuildHTTPServer, which has no TMDB holder
// arg), so the holder is injected empty via WithStubResolver and Set(inner) is
// called from cmd/server's late-bind zone once enrichBundle.TMDBHolder exists.
// Until Set, EnsureStub returns ports.ErrNotFound so an unknown tmdb id keeps the
// pre-S2 404 (enrichment disabled → no stub-create, graceful). Mirrors the S1a
// MovieFreshener / S1b MovieHotEnqueuer holders.
type MovieStubResolverHolder struct {
	mu    sync.RWMutex
	inner MovieStubResolver
}

// NewMovieStubResolverHolder constructs an unbound holder.
func NewMovieStubResolverHolder() *MovieStubResolverHolder { return &MovieStubResolverHolder{} }

// Set late-binds the real resolver. Idempotent; safe to call concurrently with
// EnsureStub (in practice Set runs at boot before the HTTP server serves).
func (h *MovieStubResolverHolder) Set(inner MovieStubResolver) {
	h.mu.Lock()
	h.inner = inner
	h.mu.Unlock()
}

// EnsureStub delegates to the bound resolver, or returns ports.ErrNotFound when
// unbound (the boot window before the TMDB holder is late-bound, or enrichment
// disabled at boot). *MovieStubResolverHolder therefore satisfies
// MovieStubResolver and is safe to inject into the usecase eagerly.
func (h *MovieStubResolverHolder) EnsureStub(ctx context.Context, tmdbID domain.TMDBID, lang string) error {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return ports.ErrNotFound
	}
	return inner.EnsureStub(ctx, tmdbID, lang)
}
