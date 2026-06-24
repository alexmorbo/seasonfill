package rest_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// Story 492 / N-1b — global cast wrapper. Tests cover the wrapper's
// OWNED logic only (400 / 404 / 500 paths + lex-first preference for
// the spliced :name + :id). The delegation body lives on the legacy
// SeriesCastHandler.Get which has its own test coverage; full
// end-to-end validation happens via the live-curl smoke step in the
// story's Verify plan.

type stubGlobalCastCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (s *stubGlobalCastCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entries, nil
}

func quietLoggerCastWrapper() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGlobalSeriesCastHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := seriesdetailrest.NewGlobalSeriesCastHandler(nil, &stubGlobalCastCacheLookup{}, nil, quietLoggerCastWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/cast", h.Get)

	for _, id := range []string{"0", "-5", "abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id+"/cast", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "id=%q", id)
	}
}

// TestGlobalSeriesCastHandler_Get_404_NoInstances_NilFallback —
// legacy behaviour: when no fallback UC is wired, the wrapper still
// returns the "series not in any library" 404. Renamed from
// ..._NoInstances after Story 535.
func TestGlobalSeriesCastHandler_Get_404_NoInstances_NilFallback(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalCastCacheLookup{entries: nil}
	h := seriesdetailrest.NewGlobalSeriesCastHandler(nil, cache, nil, quietLoggerCastWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/cast", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/cast", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "series not in any library")
}

func TestGlobalSeriesCastHandler_Get_500_CacheError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalCastCacheLookup{err: errors.New("db down")} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesCastHandler(nil, cache, nil, quietLoggerCastWrapper())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLoggerCastWrapper()))
	r.GET("/api/v1/series/:id/cast", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/cast", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGlobalSeriesCastHandler_Get_500_NilInner(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalCastCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "homelab", SonarrSeriesID: 7},
	}}
	h := seriesdetailrest.NewGlobalSeriesCastHandler(nil, cache, nil, quietLoggerCastWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/cast", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/cast", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "cast handler not wired")
}

// TestGlobalSeriesCastHandler_Get_LexFirstSplice asserts the wrapper
// picks the lex-first instance and replaces :id with the matching
// per-instance sonarr_series_id BEFORE delegating to the inner. We
// install a capturing route-level handler in front of a sentinel inner
// handler so the inner is never reached — instead the test reads
// c.Params after the wrapper returns. Without a real inner the test
// would need to construct a CastComposer with full ports; that's
// covered by the live-curl smoke step.
//
// Method: register a custom inner via a route layered after the
// wrapper that uses gin Next chain — but the wrapper invokes
// h.inner.Get(c) directly, not via gin's chain. So instead we
// install a tiny "fake" inner that captures c.Param values. To do
// that we exercise the wrapper through a capture sentinel: we set up
// a small *seriesdetailrest.SeriesCastHandler-shaped hook via a
// captured-params route middleware that runs BEFORE the wrapper, then
// the wrapper splices, then we read out c.Params from the same
// context post-hoc via Get on the context's keyed store seeded by a
// final route-level handler chained after the wrapper using gin's
// HandlerFunc list. This works because c.Params is mutated in place.
func TestGlobalSeriesCastHandler_Get_LexFirstPreference(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalCastCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "beta", SonarrSeriesID: 7},
		{InstanceName: "alpha", SonarrSeriesID: 99},
		{InstanceName: "gamma", SonarrSeriesID: 11},
	}}
	// The wrapper exits with 500 ("handler not wired") when inner is
	// nil — but BEFORE that exit, it has already done resolvePreferred
	// and set c.Params. We can't observe c.Params after that because
	// the wrapper aborts. Workaround: build the wrapper with a
	// non-nil inner that's a real SeriesCastHandler with NIL
	// composer — when invoked it will panic, but we install a recovery
	// middleware that captures c.Params + the panic and stops. This
	// way the assertion happens on real splice behaviour pre-panic.
	innerHandler := seriesdetailrest.NewSeriesCastHandler(nil, quietLoggerCastWrapper())
	h := seriesdetailrest.NewGlobalSeriesCastHandler(innerHandler, cache, nil, quietLoggerCastWrapper())
	r := gin.New()
	var capturedName, capturedID string
	r.Use(func(c *gin.Context) {
		// Wrap the response writer / params look-up so we observe
		// c.Params AFTER the wrapper has spliced. gin's recovery
		// would normally print a stack trace; we silence it.
		defer func() {
			if rec := recover(); rec != nil {
				// Capture is fine — wrapper spliced before inner
				// panicked.
				_ = rec
			}
			capturedName, _ = c.Params.Get("name")
			capturedID, _ = c.Params.Get("id")
		}()
		c.Next()
	})
	r.GET("/api/v1/series/:id/cast", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/cast?lang=ru-RU", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, "alpha", capturedName, "lex-first instance name must be spliced into :name")
	assert.Equal(t, "99", capturedID, "lex-first instance's per-instance sonarr_series_id must replace :id")
}

// Story 535 — TMDB-fallback path. stubCastFallback implements
// seriesdetailrest.TMDBFallbackCastPort.
type stubCastFallback struct {
	out *seriesdetail.CastFallbackResult
	err error
}

func (s *stubCastFallback) GetCanonicalCast(_ context.Context, _ domain.SeriesID, _ string, _ int) (*seriesdetail.CastFallbackResult, error) {
	return s.out, s.err
}

// Story 535 — fallback wired + cache empty returns 200 with canon-only payload.
func TestGlobalSeriesCastHandler_Get_TMDBFallback_Returns200(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalCastCacheLookup{entries: nil}
	fallback := &stubCastFallback{out: &seriesdetail.CastFallbackResult{
		SeriesID: 1294,
		Lang:     "ru-RU",
		Canon: series.Canon{
			ID:        1294,
			Title:     "Дом Дракона",
			Hydration: series.HydrationStub,
		},
		Cast:     []seriesdetail.CastDetail{},
		Degraded: []string{"tmdb_series"},
	}}
	h := seriesdetailrest.NewGlobalSeriesCastHandler(nil, cache, fallback, quietLoggerCastWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/cast", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/1294/cast?lang=ru-RU", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"tmdb_series"`)
	assert.Contains(t, w.Body.String(), `"series_id":1294`)
}

// Story 535 — fallback returns ports.ErrNotFound → 404 series_not_found.
func TestGlobalSeriesCastHandler_Get_TMDBFallback_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalCastCacheLookup{entries: nil}
	fallback := &stubCastFallback{err: errors.Join(errors.New("canon load"), ports.ErrNotFound)} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesCastHandler(nil, cache, fallback, quietLoggerCastWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/cast", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/cast", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"series_not_found"`)
}

// Story 535 — fallback returns generic error → 500 via error middleware.
func TestGlobalSeriesCastHandler_Get_TMDBFallback_PortError_500(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalCastCacheLookup{entries: nil}
	fallback := &stubCastFallback{err: errors.New("tmdb down")} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesCastHandler(nil, cache, fallback, quietLoggerCastWrapper())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLoggerCastWrapper()))
	r.GET("/api/v1/series/:id/cast", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/cast", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
