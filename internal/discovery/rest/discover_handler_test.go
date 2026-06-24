package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// fakeDiscoverPassthrough scripts Fetch outcomes per call.
type fakeDiscoverPassthrough struct {
	mu       sync.Mutex
	calls    atomic.Int64
	items    []disco.Item
	err      error
	delay    time.Duration
	waitSecs float64
}

func (f *fakeDiscoverPassthrough) Fetch(ctx context.Context, _ tmdb.DiscoverFilter, _ string, _ int) ([]disco.Item, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

func (f *fakeDiscoverPassthrough) LastWaitSeconds() float64 { return f.waitSecs }

type discoverFakeWarming struct{ on atomic.Bool }

func (f *discoverFakeWarming) IsWarming() bool { return f.on.Load() }

func newDiscoverHarness(t *testing.T, pass discoapp.TMDBPassthrough,
	warming discoapp.WarmingProbe, lruTTL time.Duration,
) (*gin.Engine, *cachewatch.Cache[string, []disco.Item], *discoapp.BgFetcher) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sizer := func(k string, v []disco.Item) int { return len(k) + len(v)*500 }
	lru := cachewatch.New[string, []disco.Item]("discover_test_"+t.Name(), 8, lruTTL, sizer)
	t.Cleanup(func() { _ = lru.Close() })
	bg := discoapp.NewBgFetcher(lru, pass, log)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = bg.RunWorker(ctx) }()
	h := discoveryrest.NewDiscoverHandler(lru, pass, bg, warming, nil, nil, log)
	r := gin.New()
	r.GET("/discovery/discover", h.Handle)
	return r, lru, bg
}

func TestDiscover_BadFilter_400(t *testing.T) {
	pass := &fakeDiscoverPassthrough{items: []disco.Item{}}
	r, _, _ := newDiscoverHarness(t, pass, &discoverFakeWarming{}, 1*time.Hour)

	for _, q := range []string{
		"/discovery/discover?sort_by=garbage",
		"/discovery/discover?vote_average.gte=99",
		"/discovery/discover?page=0",
		"/discovery/discover?page=501",
		"/discovery/discover?lang=zzzz",
		"/discovery/discover?with_status=99",
		"/discovery/discover?with_status_op=xor",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "GET", q, nil)
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, q)
		require.Contains(t, rec.Body.String(), `"error":"invalid_filter"`, q)
	}
}

func TestDiscover_StatusOpURLSerialisation(t *testing.T) {
	// Verifies that with_status=0,3 & with_status_op=or hits the parse
	// path AND the canonical cache key picks up the OR separator. Since
	// the handler doesn't actually call upstream when the cache holds the
	// result, two equivalent requests must share a key.
	pass := &fakeDiscoverPassthrough{items: []disco.Item{{Title: "x"}}}
	r, _, _ := newDiscoverHarness(t, pass, &discoverFakeWarming{}, 1*time.Hour)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?with_status=0,3&with_status_op=or", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?with_status=3,0&with_status_op=or", nil)
	r.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	// Order-independent cache key → second call is a hit.
	require.Contains(t, rec2.Body.String(), `"cache_status":"hit"`)
}

func TestDiscover_LRUHit_200(t *testing.T) {
	pass := &fakeDiscoverPassthrough{items: []disco.Item{{Title: "a"}, {Title: "b"}}}
	r, _, _ := newDiscoverHarness(t, pass, &discoverFakeWarming{}, 1*time.Hour)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?with_genres=18", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var first discoveryrest.DiscoverResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))
	require.Equal(t, "miss", first.CacheStatus)

	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req.Clone(t.Context()))
	require.Equal(t, http.StatusOK, rec2.Code)
	var second discoveryrest.DiscoverResponse
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &second))
	require.Equal(t, "hit", second.CacheStatus)
	require.EqualValues(t, 1, pass.calls.Load(), "second call must NOT hit upstream")
}

func TestDiscover_SyncTimeout_202(t *testing.T) {
	pass := &fakeDiscoverPassthrough{
		items: []disco.Item{{Title: "z"}},
		delay: 200 * time.Millisecond,
	}
	r, _, _ := newDiscoverHarness(t, pass, &discoverFakeWarming{}, 1*time.Hour)
	// Override the sync timeout via a wrapper that uses ctx with 10ms.
	// Easier: monkey-patch by running with a much shorter context — gin
	// honours c.Request.Context's deadline. We mimic by hitting the
	// real 5s timeout? Too slow. Instead, set delay > 5s skipped — we
	// add a per-request short ctx via gin middleware.
	rShort := gin.New()
	rShort.GET("/discovery/discover", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 50*time.Millisecond)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		r.ServeHTTP(c.Writer, c.Request)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?with_genres=18", nil)
	rShort.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)
	var resp discoveryrest.DiscoverResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "warming", resp.CacheStatus)
	require.Equal(t, 3, resp.RetryAfterSeconds)
	require.Contains(t, resp.Degraded, "tmdb_throttled")
}

func TestDiscover_TMDBHardFailure_502(t *testing.T) {
	pass := &fakeDiscoverPassthrough{err: errors.New("boom")}
	r, _, _ := newDiscoverHarness(t, pass, &discoverFakeWarming{}, 1*time.Hour)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?with_genres=18", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), `"error":"tmdb_unavailable"`)
}

func TestDiscover_DegradedThrottled_AppendedOver1s(t *testing.T) {
	pass := &fakeDiscoverPassthrough{items: []disco.Item{{Title: "p"}}, waitSecs: 2.5}
	r, _, _ := newDiscoverHarness(t, pass, &discoverFakeWarming{}, 1*time.Hour)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?with_genres=18", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp discoveryrest.DiscoverResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp.Degraded, "tmdb_throttled")
}

func TestDiscover_DegradedWarming_AppendedWhenIsWarming(t *testing.T) {
	pass := &fakeDiscoverPassthrough{items: []disco.Item{{Title: "p"}}}
	warming := &discoverFakeWarming{}
	warming.on.Store(true)
	r, _, _ := newDiscoverHarness(t, pass, warming, 1*time.Hour)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?with_genres=18", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp discoveryrest.DiscoverResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp.Degraded, "discovery_warming")
}

func TestDiscover_TTLEviction(t *testing.T) {
	pass := &fakeDiscoverPassthrough{items: []disco.Item{{Title: "a"}}}
	r, _, _ := newDiscoverHarness(t, pass, &discoverFakeWarming{}, 30*time.Millisecond)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?with_genres=18", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	time.Sleep(80 * time.Millisecond)

	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req.Clone(t.Context()))
	require.Equal(t, http.StatusOK, rec2.Code)
	require.Contains(t, rec2.Body.String(), `"cache_status":"miss"`)
	require.EqualValues(t, 2, pass.calls.Load(), "TTL expiry must force a fresh fetch")
}

func TestDiscover_OutcomeMetric_AllFourLabels(t *testing.T) {
	// Compile-time guard that all 4 outcome labels exist as exported
	// constants. The metric increment is exercised by the other tests
	// (hit / miss_sync / miss_warming / error).
	require.Equal(t, "hit", discoveryrest.OutcomeHit)
	require.Equal(t, "miss_sync", discoveryrest.OutcomeMissSync)
	require.Equal(t, "miss_warming", discoveryrest.OutcomeMissWarming)
	require.Equal(t, "error", discoveryrest.OutcomeError)
}
