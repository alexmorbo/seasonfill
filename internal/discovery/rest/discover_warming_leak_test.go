package rest_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// deadlineAwareWarmingPass returns DeadlineExceeded on the handler's
// synchronous arm (which always carries a 5s deadline) so that arm NEVER
// caches — forcing the 202 warming path — while the bg worker (background
// ctx, no deadline) returns the RAW page. This isolates the async-202 warming
// leak: the LRU can only be populated by the bg fetcher writing unfiltered
// items.
type deadlineAwareWarmingPass struct {
	calls atomic.Int64
	items []disco.Item
}

func (p *deadlineAwareWarmingPass) Fetch(ctx context.Context, _ tmdb.DiscoverFilter, _ string, _ int) ([]disco.Item, error) {
	p.calls.Add(1)
	if _, ok := ctx.Deadline(); ok {
		return nil, context.DeadlineExceeded // synchronous arm: force warming
	}
	return p.items, nil // bg worker arm: RAW page, no subtraction
}

func (p *deadlineAwareWarmingPass) LastWaitSeconds() float64 { return 0 }

// TestDiscover_WarmingPath_BgCache_SubtractsBlocked proves a hidden tmdb id
// warmed into the LRU by the async-202 bg fetcher (which stores RAW items with
// no tmdb subtraction) does NOT surface on the subsequent LRU hit — the read
// chokepoint in Handle applies the shared FilterBlocked helper. Regression
// guard for the leak: sync-timeout → 202 → bg caches raw → every later request
// is an LRU hit that would otherwise serve the hidden id forever.
func TestDiscover_WarmingPath_BgCache_SubtractsBlocked(t *testing.T) {
	t.Skip("Ф8-U-5a: blocklist read-path is a pass-through; per-user blocking restored in Ф8-U-5b")
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	blocked := shareddomain.TMDBID(777)
	kept := shareddomain.TMDBID(111)
	raw := []disco.Item{
		{SeriesID: 1, TMDBID: &blocked, Title: "BlockedWarm"},
		{SeriesID: 2, TMDBID: &kept, Title: "KeptWarm"},
	}
	pass := &deadlineAwareWarmingPass{items: raw}

	sizer := func(k string, v []disco.Item) int { return len(k) + len(v)*500 }
	lru := cachewatch.New[string, []disco.Item]("discover_warm_"+t.Name(), 8, time.Hour, sizer)
	t.Cleanup(func() { _ = lru.Close() })

	bg := discoapp.NewBgFetcher(lru, pass, log)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = bg.RunWorker(ctx) }()

	cache := discoapp.NewBlocklistCache()
	require.NoError(t, cache.Refresh(context.Background()))

	h := discoveryrest.NewDiscoverHandler(lru, pass, bg, &discoverFakeWarming{}, nil, nil, log)
	h.SetBlocklist(cache)
	r := gin.New()
	r.GET("/discovery/discover", h.Handle)

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "GET",
			"/discovery/discover?with_genres=18", nil)
		r.ServeHTTP(rec, req)
		return rec
	}

	// 1st request: synchronous arm deadlines out → 202 warming + bg enqueue.
	rec1 := do()
	require.Equal(t, http.StatusAccepted, rec1.Code)

	// Poll until the bg worker has warmed the LRU (request now hits). The
	// synchronous arm never caches (always DeadlineExceeded), so pre-warm
	// misses stay 202 and cannot mask the leak with filtered sync data.
	var hitBody []byte
	require.Eventually(t, func() bool {
		rec := do()
		if rec.Code != http.StatusOK {
			return false
		}
		var resp discoveryrest.DiscoverResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			return false
		}
		if resp.CacheStatus != "hit" {
			return false
		}
		hitBody = rec.Body.Bytes()
		return true
	}, 2*time.Second, 10*time.Millisecond)

	// The served hit came from the bg-written RAW page — the blocked id must
	// have been subtracted at read time.
	require.Equal(t, 1, discoverItemCount(t, hitBody), "blocked warmed id must be subtracted")
	require.NotContains(t, string(hitBody), "BlockedWarm")
	require.Contains(t, string(hitBody), "KeptWarm")
}
