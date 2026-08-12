package rest_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	dataports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

func tmdbItem(seriesID int, tmdbID int) disco.Item {
	id := shareddomain.TMDBID(tmdbID)
	return disco.Item{SeriesID: shareddomain.SeriesID(seriesID), TMDBID: &id, Title: "S"}
}

// ---- shared per-user blocklist fakes (Ф8-U-5b) ----

// rtUsers is a minimal dataports.UserRepository: only GetByUsername +
// FirstAdminID matter; the rest satisfy the interface via the embedded nil.
type rtUsers struct {
	dataports.UserRepository
	byName  map[string]admin.User
	adminID int64
}

func (u rtUsers) GetByUsername(_ context.Context, name string) (admin.User, error) {
	if v, ok := u.byName[name]; ok {
		return v, nil
	}
	return admin.User{}, context.Canceled // any error → resolve fails → no filtering
}

func (u rtUsers) FirstAdminID(context.Context) (int64, error) { return u.adminID, nil }

// rtLoader returns per-uid tmdb + keyword block sets.
type rtLoader struct {
	tmdb map[int64][]int64
	kw   map[int64][]int64
}

func (l rtLoader) LoadBlockSets(_ context.Context, uid int64) ([]int64, []int64, error) {
	return l.tmdb[uid], l.kw[uid], nil
}

// rtKeywords implements discoveryrest.ResultKeywords and counts calls so the
// batched (one-query-per-page) invariant can be asserted.
type rtKeywords struct {
	mu     sync.Mutex
	calls  int
	byTMDB map[int64][]int64
}

func (k *rtKeywords) ResultKeywords(_ context.Context, _ []int64) (map[int64][]int64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls++
	return k.byTMDB, nil
}

func (k *rtKeywords) callCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.calls
}

func rtLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// testUserMiddleware seeds middleware.UsernameContextKey from an X-Test-User
// header so a single engine can serve requests as different users hitting the
// SAME shared LRU.
func testUserMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if u := c.GetHeader("X-Test-User"); u != "" {
			c.Set(middleware.UsernameContextKey, u)
		}
	}
}

// doAs issues a request as the given user (empty → no user on context).
func doAs(r *gin.Engine, method, path, user string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	r.ServeHTTP(rec, req)
	return rec
}

// discoverItemCount unmarshals an envelope and returns the item count.
func discoverItemCount(t *testing.T, body []byte) int {
	t.Helper()
	var resp struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	return len(resp.Items)
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

// ---- curated (DiscoveryHandler) per-user tmdb block ----

// TestDiscovery_Popular_FilterBlocked_PerUser proves the curated reader
// subtracts a per-user blocked tmdb_id: hidden for the blocking user, visible
// to a user without the block.
func TestDiscovery_Popular_FilterBlocked_PerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := newFakeRepo()
	blocked := shareddomain.TMDBID(42)
	kept := shareddomain.TMDBID(111)
	items := []disco.Item{
		{SeriesID: 1, TMDBID: &blocked, Title: "BlockedOne"},
		{SeriesID: 2, TMDBID: &kept, Title: "KeptOne"},
		{SeriesID: 3, Title: "NoTMDBStub"},
	}
	now := time.Now()
	repo.setPage(disco.KindPopular, "", "en-US",
		disco.Page{Items: items, Total: 3, RefreshedAt: now}, false, now)

	users := rtUsers{byName: map[string]admin.User{"userA": {ID: 1}, "userB": {ID: 2}}, adminID: 1}
	loader := rtLoader{tmdb: map[int64][]int64{1: {42}}} // only userA blocks 42
	ubf := discoveryrest.NewUserBlockFilterForWiring(users, loader, &rtKeywords{}, rtLog())

	h := discoveryrest.NewDiscoveryHandler(
		repo, &fakeWarming{}, &fakeRefresh{},
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		nil, nil, nil, rtLog(),
	)
	h.SetUserBlocks(ubf)
	r := gin.New()
	r.Use(testUserMiddleware())
	r.GET("/discovery/popular", h.Popular)

	// userA — 42 hidden.
	recA := doAs(r, "GET", "/discovery/popular?lang=en-US", "userA")
	require.Equal(t, http.StatusOK, recA.Code)
	require.Equal(t, 2, discoverItemCount(t, recA.Body.Bytes()))
	require.NotContains(t, recA.Body.String(), "BlockedOne")
	require.Contains(t, recA.Body.String(), "KeptOne")
	require.Contains(t, recA.Body.String(), "NoTMDBStub")

	// userB — no block → sees all three.
	recB := doAs(r, "GET", "/discovery/popular?lang=en-US", "userB")
	require.Equal(t, http.StatusOK, recB.Code)
	require.Equal(t, 3, discoverItemCount(t, recB.Body.Bytes()))
	require.Contains(t, recB.Body.String(), "BlockedOne")
}

// TestDiscovery_Popular_NoUser_FiltersNothing proves a request with no user on
// the context applies no blocklist (no leak of another user's set, no panic).
func TestDiscovery_Popular_NoUser_FiltersNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := newFakeRepo()
	blocked := shareddomain.TMDBID(42)
	items := []disco.Item{{SeriesID: 1, TMDBID: &blocked, Title: "BlockedOne"}}
	now := time.Now()
	repo.setPage(disco.KindPopular, "", "en-US",
		disco.Page{Items: items, Total: 1, RefreshedAt: now}, false, now)

	users := rtUsers{byName: map[string]admin.User{"userA": {ID: 1}}, adminID: 1}
	loader := rtLoader{tmdb: map[int64][]int64{1: {42}}}
	ubf := discoveryrest.NewUserBlockFilterForWiring(users, loader, &rtKeywords{}, rtLog())

	h := discoveryrest.NewDiscoveryHandler(
		repo, &fakeWarming{}, &fakeRefresh{},
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		nil, nil, nil, rtLog(),
	)
	h.SetUserBlocks(ubf)
	r := gin.New()
	r.Use(testUserMiddleware())
	r.GET("/discovery/popular", h.Popular)

	rec := doAs(r, "GET", "/discovery/popular?lang=en-US", "") // no user
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, discoverItemCount(t, rec.Body.Bytes()), "no-user request filters nothing")
	require.Contains(t, rec.Body.String(), "BlockedOne")
}

// TestDiscovery_Popular_KeywordBlock_PerUser replaces the removed keyword-fold
// test: userA blocks a TMDB keyword; an ENRICHED result carrying it is dropped
// for A only; an un-enriched result (no keyword row) is kept; userB sees both.
// The batched keyword lookup must issue EXACTLY ONE query per page.
func TestDiscovery_Popular_KeywordBlock_PerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := newFakeRepo()
	enriched := shareddomain.TMDBID(10) // has keyword 500
	unEnriched := shareddomain.TMDBID(20)
	items := []disco.Item{
		{SeriesID: 1, TMDBID: &enriched, Title: "EnrichedWithKw"},
		{SeriesID: 2, TMDBID: &unEnriched, Title: "UnEnriched"},
	}
	now := time.Now()
	repo.setPage(disco.KindPopular, "", "en-US",
		disco.Page{Items: items, Total: 2, RefreshedAt: now}, false, now)

	users := rtUsers{byName: map[string]admin.User{"userA": {ID: 1}, "userB": {ID: 2}}, adminID: 1}
	loader := rtLoader{kw: map[int64][]int64{1: {500}}} // userA blocks keyword 500
	kw := &rtKeywords{byTMDB: map[int64][]int64{10: {500}}}
	ubf := discoveryrest.NewUserBlockFilterForWiring(users, loader, kw, rtLog())

	h := discoveryrest.NewDiscoveryHandler(
		repo, &fakeWarming{}, &fakeRefresh{},
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		nil, nil, nil, rtLog(),
	)
	h.SetUserBlocks(ubf)
	r := gin.New()
	r.Use(testUserMiddleware())
	r.GET("/discovery/popular", h.Popular)

	// userA — enriched (keyword 500) dropped; un-enriched kept.
	recA := doAs(r, "GET", "/discovery/popular?lang=en-US", "userA")
	require.Equal(t, http.StatusOK, recA.Code)
	require.Equal(t, 1, discoverItemCount(t, recA.Body.Bytes()))
	require.NotContains(t, recA.Body.String(), "EnrichedWithKw")
	require.Contains(t, recA.Body.String(), "UnEnriched", "un-enriched result kept (accepted leak)")
	require.Equal(t, 1, kw.callCount(), "keyword lookup must be batched: one query per page")

	// userB — no keyword block → sees both.
	recB := doAs(r, "GET", "/discovery/popular?lang=en-US", "userB")
	require.Equal(t, http.StatusOK, recB.Code)
	require.Equal(t, 2, discoverItemCount(t, recB.Body.Bytes()))
}

// ---- passthrough (DiscoverHandler) per-user + RAW-LRU regression ----

func newPerUserDiscoverHarness(t *testing.T, pass discoapp.TMDBPassthrough, users rtUsers, loader rtLoader, kw discoveryrest.ResultKeywords) (*gin.Engine, *cachewatch.Cache[string, []disco.Item]) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := rtLog()
	sizer := func(k string, v []disco.Item) int { return len(k) + len(v)*500 }
	lru := cachewatch.New[string, []disco.Item]("peruser_discover_"+t.Name(), 8, time.Hour, sizer)
	t.Cleanup(func() { _ = lru.Close() })
	bg := discoapp.NewBgFetcher(lru, pass, log)
	h := discoveryrest.NewDiscoverHandler(lru, pass, bg, &discoverFakeWarming{}, nil, nil, log)
	h.SetUserBlocks(discoveryrest.NewUserBlockFilterForWiring(users, loader, kw, log))
	r := gin.New()
	r.Use(testUserMiddleware())
	r.GET("/discovery/discover", h.Handle)
	return r, lru
}

// TestDiscover_Passthrough_PerUser_RawLRU proves the passthrough hides a
// per-user blocked tmdb id for the blocking user while the SHARED LRU stores the
// RAW page — a second user (no block) hitting the same cache entry still sees
// the id. This is the warming-leak regression restated per-user.
func TestDiscover_Passthrough_PerUser_RawLRU(t *testing.T) {
	page := []disco.Item{tmdbItem(1, 42), tmdbItem(2, 111)}
	pass := &pageAwarePass{byPage: map[int][]disco.Item{1: page}}

	users := rtUsers{byName: map[string]admin.User{"userA": {ID: 1}, "userB": {ID: 2}}, adminID: 1}
	loader := rtLoader{tmdb: map[int64][]int64{1: {42}}} // only userA blocks 42
	r, _ := newPerUserDiscoverHarness(t, pass, users, loader, &rtKeywords{})

	// userA: sync miss → RAW cached, response filtered (42 hidden).
	recA := doAs(r, "GET", "/discovery/discover?page=1", "userA")
	require.Equal(t, http.StatusOK, recA.Code)
	require.Equal(t, 1, discoverItemCount(t, recA.Body.Bytes()))
	require.NotContains(t, recA.Body.String(), "\"tmdb_id\":42")

	// userB: LRU hit on the SAME (RAW) page → sees 42 → proves RAW storage.
	recB := doAs(r, "GET", "/discovery/discover?page=1", "userB")
	require.Equal(t, http.StatusOK, recB.Code)
	var respB discoveryrest.DiscoverResponse
	require.NoError(t, json.Unmarshal(recB.Body.Bytes(), &respB))
	require.Equal(t, "hit", respB.CacheStatus, "userB must hit the shared cache")
	require.Equal(t, 2, len(respB.Items), "the shared LRU stored the RAW (unfiltered) page")
}

// TestDiscover_Backfill_TopsUpTo20_PerUser proves the passthrough tops a
// per-user filtered short page back to PAGE_SIZE by fetching the next TMDB page.
func TestDiscover_Backfill_TopsUpTo20_PerUser(t *testing.T) {
	page1 := make([]disco.Item, 0, 20)
	for i := 1; i <= 20; i++ {
		page1 = append(page1, tmdbItem(i, i)) // tmdb ids 1..20
	}
	page2 := make([]disco.Item, 0, 20)
	for i := 1; i <= 20; i++ {
		page2 = append(page2, tmdbItem(100+i, 100+i)) // unblocked
	}
	pass := &pageAwarePass{byPage: map[int][]disco.Item{1: page1, 2: page2}}

	users := rtUsers{byName: map[string]admin.User{"userA": {ID: 1}, "userB": {ID: 2}}, adminID: 1}
	loader := rtLoader{tmdb: map[int64][]int64{1: {1, 2, 3, 4, 5}}} // userA blocks 1..5
	r, _ := newPerUserDiscoverHarness(t, pass, users, loader, &rtKeywords{})

	recA := doAs(r, "GET", "/discovery/discover?page=1", "userA")
	require.Equal(t, http.StatusOK, recA.Code)
	require.Equal(t, 20, discoverItemCount(t, recA.Body.Bytes()), "backfill must top up to 20")
	require.True(t, pass.fetchedPage(2), "backfill must fetch page 2")
}

// TestDiscover_Backfill_ShortFillWhenExhausted_PerUser proves the reader returns
// fewer than 20 without error when TMDB runs out during per-user backfill.
func TestDiscover_Backfill_ShortFillWhenExhausted_PerUser(t *testing.T) {
	page1 := make([]disco.Item, 0, 20)
	for i := 1; i <= 20; i++ {
		page1 = append(page1, tmdbItem(i, i)) // 1..20, block 1..5 → 15 remain
	}
	page2 := []disco.Item{tmdbItem(101, 101), tmdbItem(102, 102), tmdbItem(103, 103)} // +3 → 18
	pass := &pageAwarePass{byPage: map[int][]disco.Item{1: page1, 2: page2, 3: {}}}

	users := rtUsers{byName: map[string]admin.User{"userA": {ID: 1}}, adminID: 1}
	loader := rtLoader{tmdb: map[int64][]int64{1: {1, 2, 3, 4, 5}}}
	r, _ := newPerUserDiscoverHarness(t, pass, users, loader, &rtKeywords{})

	recA := doAs(r, "GET", "/discovery/discover?page=1", "userA")
	require.Equal(t, http.StatusOK, recA.Code)
	got := discoverItemCount(t, recA.Body.Bytes())
	require.Less(t, got, 20, "short-fill returns fewer than 20 when TMDB is exhausted")
	require.Equal(t, 18, got)
}
