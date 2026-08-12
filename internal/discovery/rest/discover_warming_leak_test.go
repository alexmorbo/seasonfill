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

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
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

func (p *deadlineAwareWarmingPass) Fetch(ctx context.Context, _ tmdb.DiscoverFilter, _ string, page int) ([]disco.Item, error) {
	p.calls.Add(1)
	if _, ok := ctx.Deadline(); ok {
		return nil, context.DeadlineExceeded // synchronous arm: force warming
	}
	if page != 1 {
		return nil, nil // backfill pages are TMDB's tail — no more items
	}
	return p.items, nil // bg worker arm: RAW page-1, no subtraction
}

func (p *deadlineAwareWarmingPass) LastWaitSeconds() float64 { return 0 }

// TestDiscover_WarmingPath_BgCache_SubtractsBlocked_PerUser proves a hidden
// tmdb id warmed into the SHARED LRU by the async-202 bg fetcher (which stores
// RAW items with NO subtraction) does NOT surface on the blocking user's LRU
// hit — the read chokepoint applies the per-user filter — while the LRU still
// holds the RAW page (a user WITHOUT the block sees the id on the same cache
// entry). Ф8-U-5b regression guard for the warming leak restated per-user.
func TestDiscover_WarmingPath_BgCache_SubtractsBlocked_PerUser(t *testing.T) {
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

	users := rtUsers{byName: map[string]admin.User{"userA": {ID: 1}, "userB": {ID: 2}}, adminID: 1}
	loader := rtLoader{tmdb: map[int64][]int64{1: {777}}} // only userA blocks 777

	h := discoveryrest.NewDiscoverHandler(lru, pass, bg, &discoverFakeWarming{}, nil, nil, log)
	h.SetUserBlocks(discoveryrest.NewUserBlockFilterForWiring(users, loader, &rtKeywords{}, log))
	r := gin.New()
	r.Use(testUserMiddleware())
	r.GET("/discovery/discover", h.Handle)

	doUser := func(user string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "GET",
			"/discovery/discover?with_genres=18", nil)
		req.Header.Set("X-Test-User", user)
		r.ServeHTTP(rec, req)
		return rec
	}

	// 1st request (userA): synchronous arm deadlines out → 202 warming + bg enqueue.
	rec1 := doUser("userA")
	require.Equal(t, http.StatusAccepted, rec1.Code)

	// Poll until the bg worker has warmed the shared LRU (userA now hits). The
	// synchronous arm never caches (always DeadlineExceeded), so pre-warm misses
	// stay 202 and cannot mask the leak with filtered sync data.
	var hitBody []byte
	require.Eventually(t, func() bool {
		rec := doUser("userA")
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

	// userA's hit came from the bg-written RAW page — 777 subtracted per-user.
	require.Equal(t, 1, discoverItemCount(t, hitBody), "blocked warmed id must be subtracted for userA")
	require.NotContains(t, string(hitBody), "BlockedWarm")
	require.Contains(t, string(hitBody), "KeptWarm")

	// userB (no block) hits the SAME cache entry → sees 777 → the LRU is RAW.
	recB := doUser("userB")
	require.Equal(t, http.StatusOK, recB.Code)
	var respB discoveryrest.DiscoverResponse
	require.NoError(t, json.Unmarshal(recB.Body.Bytes(), &respB))
	require.Equal(t, "hit", respB.CacheStatus)
	require.Equal(t, 2, len(respB.Items), "shared LRU stores the RAW warmed page")
	require.Contains(t, recB.Body.String(), "BlockedWarm")
}
