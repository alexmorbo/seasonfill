package rest_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func tmdbItem(seriesID int, tmdbID int) disco.Item {
	id := shareddomain.TMDBID(tmdbID)
	return disco.Item{SeriesID: shareddomain.SeriesID(seriesID), TMDBID: &id, Title: "S"}
}

// TestDiscovery_Popular_FilterBlocked proves the curated reader subtracts a
// blocked tmdb_id via the shared FilterBlocked chokepoint in readAndProject.
func TestDiscovery_Popular_FilterBlocked(t *testing.T) {
	t.Skip("Ф8-U-5a: blocklist read-path is a pass-through; per-user blocking restored in Ф8-U-5b")
	repo := newFakeRepo()
	blocked := shareddomain.TMDBID(777)
	kept := shareddomain.TMDBID(111)
	items := []disco.Item{
		{SeriesID: 1, TMDBID: &blocked, Title: "BlockedOne"},
		{SeriesID: 2, TMDBID: &kept, Title: "KeptOne"},
		{SeriesID: 3, Title: "NoTMDBStub"},
	}
	now := time.Now()
	repo.setPage(disco.KindPopular, "", "en-US",
		disco.Page{Items: items, Total: 3, RefreshedAt: now}, false, now)

	cache := discoapp.NewBlocklistCache()
	require.NoError(t, cache.Refresh(context.Background()))

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := discoveryrest.NewDiscoveryHandler(
		repo, &fakeWarming{}, &fakeRefresh{},
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		nil, nil, nil, log,
		discoveryrest.WithBlocklist(cache),
	)
	r := gin.New()
	r.GET("/discovery/popular", h.Popular)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/popular?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2, "blocked tmdb_id must be subtracted")
	require.NotContains(t, rec.Body.String(), "BlockedOne")
	require.Contains(t, rec.Body.String(), "KeptOne")
	require.Contains(t, rec.Body.String(), "NoTMDBStub")
}

// pageAwarePass returns per-page item sets and records which pages were fetched.
type pageAwarePass struct {
	mu      sync.Mutex
	byPage  map[int][]disco.Item
	fetched map[int]int
}

func (p *pageAwarePass) Fetch(_ context.Context, _ tmdb.DiscoverFilter, _ string, page int) ([]disco.Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fetched == nil {
		p.fetched = map[int]int{}
	}
	p.fetched[page]++
	return p.byPage[page], nil
}

func (p *pageAwarePass) LastWaitSeconds() float64 { return 0 }

func (p *pageAwarePass) fetchedPage(page int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetched[page] > 0
}

func newBlocklistDiscoverHarness(t *testing.T, pass discoapp.TMDBPassthrough, cache *discoapp.BlocklistCache) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sizer := func(k string, v []disco.Item) int { return len(k) + len(v)*500 }
	lru := cachewatch.New[string, []disco.Item]("blocklist_discover_"+t.Name(), 8, time.Hour, sizer)
	t.Cleanup(func() { _ = lru.Close() })
	bg := discoapp.NewBgFetcher(lru, pass, log)
	h := discoveryrest.NewDiscoverHandler(lru, pass, bg, &discoverFakeWarming{}, nil, nil, log)
	h.SetBlocklist(cache)
	r := gin.New()
	r.GET("/discovery/discover", h.Handle)
	return r
}

func discoverItemCount(t *testing.T, body []byte) int {
	t.Helper()
	var resp struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	return len(resp.Items)
}

// filterCapturePass records the DiscoverFilter of the most recent Fetch so
// tests can assert the keyword blocklist was folded into WithoutKeywords.
type filterCapturePass struct {
	mu       sync.Mutex
	last     tmdb.DiscoverFilter
	captured bool
}

func (p *filterCapturePass) Fetch(_ context.Context, filter tmdb.DiscoverFilter, _ string, _ int) ([]disco.Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last = filter
	p.captured = true
	return nil, nil // empty page — the test only inspects the outgoing filter
}

func (p *filterCapturePass) LastWaitSeconds() float64 { return 0 }

func (p *filterCapturePass) lastFilter() tmdb.DiscoverFilter {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// TestDiscover_Passthrough_InjectsBlockedKeywords proves the /discovery/discover
// passthrough folds the blocked keyword ids into the outgoing DiscoverFilter's
// WithoutKeywords (union + dedupe with the caller-supplied ?without_keywords).
func TestDiscover_Passthrough_InjectsBlockedKeywords(t *testing.T) {
	t.Skip("Ф8-U-5a: blocklist read-path is a pass-through; per-user blocking restored in Ф8-U-5b")
	pass := &filterCapturePass{}
	cache := discoapp.NewBlocklistCache()
	require.NoError(t, cache.Refresh(context.Background()))

	r := newBlocklistDiscoverHarness(t, pass, cache)
	rec := httptest.NewRecorder()
	// Caller supplies without_keywords=55,999 — 55 is also blocked → dedupe.
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?page=1&without_keywords=55,999", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.True(t, pass.captured, "passthrough Fetch must have been called")
	got := pass.lastFilter().WithoutKeywords
	require.ElementsMatch(t, []int{55, 999, 210024}, got,
		"blocked keyword ids must be unioned with caller-supplied without_keywords")
}

// TestDiscover_Backfill_TopsUpTo20 proves the passthrough reader tops a
// filtered short page back to PAGE_SIZE by fetching the next TMDB page.
func TestDiscover_Backfill_TopsUpTo20(t *testing.T) {
	t.Skip("Ф8-U-5a: blocklist read-path is a pass-through; per-user blocking restored in Ф8-U-5b")
	page1 := make([]disco.Item, 0, 20)
	for i := 1; i <= 20; i++ {
		page1 = append(page1, tmdbItem(i, i)) // tmdb ids 1..20
	}
	page2 := make([]disco.Item, 0, 20)
	for i := 1; i <= 20; i++ {
		page2 = append(page2, tmdbItem(100+i, 100+i)) // unblocked
	}
	pass := &pageAwarePass{byPage: map[int][]disco.Item{1: page1, 2: page2}}

	cache := discoapp.NewBlocklistCache()
	require.NoError(t, cache.Refresh(context.Background()))

	r := newBlocklistDiscoverHarness(t, pass, cache)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/discover?page=1", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 20, discoverItemCount(t, rec.Body.Bytes()), "backfill must top up to 20")
	require.True(t, pass.fetchedPage(2), "backfill must fetch page 2")
}

// TestDiscover_Backfill_ShortFillWhenExhausted proves the reader returns
// fewer than 20 without error when TMDB runs out during backfill.
func TestDiscover_Backfill_ShortFillWhenExhausted(t *testing.T) {
	t.Skip("Ф8-U-5a: blocklist read-path is a pass-through; per-user blocking restored in Ф8-U-5b")
	page1 := make([]disco.Item, 0, 20)
	for i := 1; i <= 20; i++ {
		page1 = append(page1, tmdbItem(i, i)) // 1..20, block 1..5 → 15 remain
	}
	page2 := []disco.Item{tmdbItem(101, 101), tmdbItem(102, 102), tmdbItem(103, 103)} // +3 → 18
	pass := &pageAwarePass{byPage: map[int][]disco.Item{1: page1, 2: page2, 3: {}}}

	cache := discoapp.NewBlocklistCache()
	require.NoError(t, cache.Refresh(context.Background()))

	r := newBlocklistDiscoverHarness(t, pass, cache)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/discover?page=1", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	got := discoverItemCount(t, rec.Body.Bytes())
	require.Less(t, got, 20, "short-fill returns fewer than 20 when TMDB is exhausted")
	require.Equal(t, 18, got)
	require.True(t, strings.HasPrefix(rec.Body.String(), "{"), "still a valid envelope")
}
