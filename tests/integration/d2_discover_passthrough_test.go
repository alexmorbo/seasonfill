//go:build integration

package integration

import (
	"context"
	"encoding/json"
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
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type stubCounter struct {
	mu  sync.Mutex
	ids map[shareddomain.TMDBID]shareddomain.SeriesID
	n   atomic.Int64
}

func (s *stubCounter) EnsureStub(_ context.Context, tmdbID shareddomain.TMDBID, _ string, _, _ *string) (shareddomain.SeriesID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.ids[tmdbID]; ok {
		return v, nil
	}
	if s.ids == nil {
		s.ids = map[shareddomain.TMDBID]shareddomain.SeriesID{}
	}
	n := s.n.Add(1)
	sid := shareddomain.SeriesID(n)
	s.ids[tmdbID] = sid
	return sid, nil
}

type scriptedTMDB struct {
	calls atomic.Int64
	resp  *tmdb.TVListResponse
}

func (s *scriptedTMDB) DiscoverTV(_ context.Context, _ tmdb.DiscoverFilter, _ int) (*tmdb.TVListResponse, error) {
	s.calls.Add(1)
	return s.resp, nil
}

type stubWarming struct{}

func (stubWarming) IsWarming() bool { return false }

func TestD2_DiscoverPassthrough_CacheHitAcrossRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	tmdbFake := &scriptedTMDB{resp: &tmdb.TVListResponse{Results: []tmdb.TVListEntry{
		{ID: 100, Name: "Alpha", FirstAirDate: "2020-01-01"},
		{ID: 101, Name: "Beta", FirstAirDate: "2021-02-02"},
		{ID: 102, Name: "Gamma", FirstAirDate: "2022-03-03"},
		{ID: 103, Name: "Delta", FirstAirDate: "2023-04-04"},
		{ID: 104, Name: "Epsilon", FirstAirDate: "2024-05-05"},
	}}}
	stubs := &stubCounter{}
	sizer := func(k string, v []disco.Item) int { return len(k) + len(v)*500 }
	lru := cachewatch.New[string, []disco.Item]("discover_int", 16, 1*time.Hour, sizer)
	t.Cleanup(func() { _ = lru.Close() })

	pass := discoapp.NewTMDBPassthrough(tmdbFake, stubs, log)
	bg := discoapp.NewBgFetcher(lru, pass, log)
	ctx, cancel := context.WithCancel(t.Context())
	bgDone := make(chan struct{})
	go func() { defer close(bgDone); _ = bg.RunWorker(ctx) }()

	h := discoveryrest.NewDiscoverHandler(lru, pass, bg, stubWarming{}, log)
	r := gin.New()
	r.GET("/discovery/discover", h.Handle)

	// First request — miss → 5 items.
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/discover?with_genres=18&first_air_date.gte=2020-01-01", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var first discoveryrest.DiscoverResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))
	require.Equal(t, "miss", first.CacheStatus)
	require.Len(t, first.Items, 5)
	require.EqualValues(t, 1, tmdbFake.calls.Load(), "first call must hit upstream")
	require.EqualValues(t, 5, stubs.n.Load(), "stub upsert ran once per result")

	// Second request — hit.
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req.Clone(t.Context()))
	require.Equal(t, http.StatusOK, rec2.Code)
	var second discoveryrest.DiscoverResponse
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &second))
	require.Equal(t, "hit", second.CacheStatus)
	require.EqualValues(t, 1, tmdbFake.calls.Load(), "second call must NOT hit upstream")
	require.EqualValues(t, 5, stubs.n.Load(), "no additional stub-upserts on cache hit")

	// Cancel ctx → BgFetcher exits cleanly.
	cancel()
	select {
	case <-bgDone:
	case <-time.After(1 * time.Second):
		t.Fatal("BgFetcher did not exit within 1s after ctx cancel")
	}
}
