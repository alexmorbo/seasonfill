package rest

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

func TestInstancesHandler_List_AfterPreflight(t *testing.T) {
	// Use the same scaffolding as health_test.go — one checker, two clients,
	// one of which errors.
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{
		&fakeSonarr{name: "ok"},
		&fakeSonarr{name: "broken", err: errors.New("nope")},
	})
	c.Preflight(context.Background())

	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, InstanceRegistry{}, nil).List)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Instances []map[string]any `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 2)
	names := map[string]string{}
	for _, i := range body.Instances {
		names[i["name"].(string)] = i["health"].(string)
	}
	assert.Equal(t, "Available", names["ok"])
	assert.NotEqual(t, "Available", names["broken"])
}

func TestInstancesHandler_List_Empty(t *testing.T) {
	c := healthcheck.New(openInstancesDB(t), nil)
	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, InstanceRegistry{}, nil).List)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Instances []map[string]any `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.Instances)
}

// TestInstancesHandler_LastCheckAt_OmittedWhenNeverChecked verifies that
// last_check_at is absent (omitempty) in the JSON when an instance has never
// been preflighted, preventing the "0001-01-01T00:00:00Z" zero-value leak.
func TestInstancesHandler_LastCheckAt_OmittedWhenNeverChecked(t *testing.T) {
	// Register one instance but do NOT call Preflight — LastCheckAt stays zero.
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{
		&fakeSonarr{name: "unchecked"},
	})

	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, InstanceRegistry{}, nil).List)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Instances []map[string]any `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	// The unchecked instance snapshot is only emitted after registration via
	// Snapshot(). If the checker returns no snapshots for un-preflighted
	// instances the list may be empty — that's fine; the important contract is
	// that no entry carries the zero-time value.
	for _, inst := range body.Instances {
		_, hasKey := inst["last_check_at"]
		assert.False(t, hasKey, "last_check_at must be absent for never-checked instance %v", inst["name"])
	}
}

func openInstancesDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	return db
}

type missingFakeSonarr struct {
	*fakeSonarr
	all []series.Series
	err error
	// epsBySeries: seriesID → ALL episodes (every season). The Missing
	// handler now calls ListEpisodesBySeries once per series and
	// filters by season in Go.
	epsBySeries map[shareddomain.SonarrSeriesID][]series.Episode
	// upstreamDelay simulates per-call Sonarr latency so concurrency
	// tests can assert wall-clock budget (sequential vs. parallel).
	upstreamDelay time.Duration
	// listEpisodesBySeriesIDs tracks every ListEpisodesBySeries call so
	// tests can assert per-request fan-out is bounded by series count
	// (NOT season count) — the perf invariant for the embed path.
	mu                      sync.Mutex
	listEpisodesBySeriesIDs []shareddomain.SonarrSeriesID
	// errBySeries lets tests inject per-series failures so the partial-
	// failure path (one series fails, others succeed) is testable.
	errBySeries map[shareddomain.SonarrSeriesID]error
}

func (m *missingFakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.all, nil
}

// ListEpisodes — drill-endpoint path only; the Missing list handler
// no longer calls it (replaced by ListEpisodesBySeries with in-Go
// season filtering).
func (m *missingFakeSonarr) ListEpisodes(_ context.Context, _ shareddomain.SonarrSeriesID, _ int) ([]series.Episode, error) {
	return nil, nil
}

func (m *missingFakeSonarr) ListEpisodesBySeries(ctx context.Context, seriesID shareddomain.SonarrSeriesID) ([]series.Episode, error) {
	if m.upstreamDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.upstreamDelay):
		}
	}
	m.mu.Lock()
	m.listEpisodesBySeriesIDs = append(m.listEpisodesBySeriesIDs, seriesID)
	m.mu.Unlock()
	if err, ok := m.errBySeries[seriesID]; ok {
		return nil, err
	}
	if m.epsBySeries == nil {
		return nil, nil
	}
	return m.epsBySeries[seriesID], nil
}

func (m *missingFakeSonarr) listEpisodesBySeriesCalls() []shareddomain.SonarrSeriesID {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]shareddomain.SonarrSeriesID, len(m.listEpisodesBySeriesIDs))
	copy(out, m.listEpisodesBySeriesIDs)
	return out
}

// doMissing wires a one-route gin engine and returns the recorder.
func doMissing(t *testing.T, name string, clients map[string]ports.SonarrClient, modes map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return doMissingWithCache(t, name, clients, modes, nil)
}

// doMissingWithCache mirrors doMissing but wires a SeriesCacheRepository
// stub so 041g enrichment tests can drive the join path. Cache=nil
// reproduces the pre-041g behaviour (no enrichment, every row's
// TitleSlug/Year/PosterHash stays at zero value).
func doMissingWithCache(t *testing.T, name string, clients map[string]ports.SonarrClient, modes map[string]string, cache ports.SeriesCacheRepository) *httptest.ResponseRecorder {
	t.Helper()
	c := healthcheck.New(openInstancesDB(t), nil)
	r := gin.New()
	h := NewInstancesHandler(c, buildRegistry(clients, modes, nil), nil)
	if cache != nil {
		h = h.WithSeriesCache(cache)
	}
	r.GET("/api/v1/instances/:name/missing", h.Missing)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/"+name+"/missing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestInstancesHandler_Missing_OK(t *testing.T) {
	mf := &missingFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		all: []series.Series{
			{ID: 1, Title: "Severance", Monitored: true,
				Statistics: series.Statistics{EpisodeCount: 18, EpisodeFileCount: 10},
				Seasons: []series.Season{
					{Number: 1, Monitored: true, Statistics: series.Statistics{EpisodeCount: 9, EpisodeFileCount: 9}},
					{Number: 2, Monitored: true, Statistics: series.Statistics{EpisodeCount: 9, EpisodeFileCount: 1}},
				}},
			{ID: 2, Title: "Caught up", Monitored: true,
				Statistics: series.Statistics{EpisodeCount: 12, EpisodeFileCount: 12},
				Seasons: []series.Season{
					{Number: 1, Monitored: true, Statistics: series.Statistics{EpisodeCount: 12, EpisodeFileCount: 12}},
				}},
		},
	}
	w := doMissing(t, "alpha",
		map[string]ports.SonarrClient{"alpha": mf},
		map[string]string{"alpha": "manual"})
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Items []struct {
			SeriesID          int    `json:"series_id"`
			Title             string `json:"title"`
			Monitored         bool   `json:"monitored"`
			TotalMissingAired int    `json:"total_missing_aired"`
			Seasons           []struct {
				SeasonNumber      int `json:"season_number"`
				MissingAiredCount int `json:"missing_aired_count"`
			} `json:"seasons"`
		} `json:"items"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, 1, body.Total, "only Severance has aired-missing")
	require.Len(t, body.Items, 1)
	assert.Equal(t, 1, body.Items[0].SeriesID)
	assert.Equal(t, 8, body.Items[0].TotalMissingAired)
	require.Len(t, body.Items[0].Seasons, 1, "complete S1 is filtered out")
	assert.Equal(t, 2, body.Items[0].Seasons[0].SeasonNumber)
	assert.Equal(t, 8, body.Items[0].Seasons[0].MissingAiredCount)
}

func TestInstancesHandler_Missing_UnknownInstance(t *testing.T) {
	w := doMissing(t, "ghost", map[string]ports.SonarrClient{}, map[string]string{})
	require.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "ghost")
}

func TestInstancesHandler_Missing_FiltersUnmonitored(t *testing.T) {
	mf := &missingFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		all: []series.Series{{
			ID: 1, Title: "Unmonitored", Monitored: false,
			Statistics: series.Statistics{EpisodeCount: 10, EpisodeFileCount: 0},
			Seasons: []series.Season{
				{Number: 1, Monitored: false, Statistics: series.Statistics{EpisodeCount: 10}},
			},
		}},
	}
	w := doMissing(t, "alpha",
		map[string]ports.SonarrClient{"alpha": mf},
		map[string]string{"alpha": "auto"})
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 0, body.Total, "unmonitored series excluded")
}

// doMissingWithEpisodesCache wires both the SeriesCacheRepository and
// the in-process EpisodesCache. Episode-cache=nil reproduces the cold-
// path (every call hits upstream); pass an LRU to drive cache-hit
// assertions.
func doMissingWithEpisodesCache(
	t *testing.T,
	name string,
	clients map[string]ports.SonarrClient,
	modes map[string]string,
	episodesCache sonarr.EpisodesCache,
) *httptest.ResponseRecorder {
	t.Helper()
	c := healthcheck.New(openInstancesDB(t), nil)
	r := gin.New()
	h := NewInstancesHandler(c, buildRegistry(clients, modes, nil), nil)
	if episodesCache != nil {
		h = h.WithEpisodesCache(episodesCache)
	}
	r.GET("/api/v1/instances/:name/missing", h.Missing)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/"+name+"/missing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestInstancesHandler_Missing_EmbedsEpisodesPerSeries asserts the
// post-revert behaviour: the queue Missing handler re-embeds per-
// episode presence inline for seasons under the embed cap, but fans
// out ONCE per series (not once per season) by calling the new
// ListEpisodesBySeries endpoint. The fan-out is the perf invariant
// that keeps a backlog with N missing seasons at O(series), not
// O(seasons) — the pre-Story-101 regression was 9 series × 3 seasons
// = 27 round-trips per /missing request.
func TestInstancesHandler_Missing_EmbedsEpisodesPerSeries(t *testing.T) {
	past := time.Now().Add(-7 * 24 * time.Hour)
	future := time.Now().Add(7 * 24 * time.Hour)
	small := series.Statistics{EpisodeCount: 10, EpisodeFileCount: 6, Aired: 10}
	huge := series.Statistics{EpisodeCount: 500, EpisodeFileCount: 100, Aired: 500}
	legacy := series.Statistics{EpisodeCount: 5, EpisodeFileCount: 0}
	mf := &missingFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		all: []series.Series{
			{ID: 1, Title: "Standard", Monitored: true,
				Statistics: series.Statistics{EpisodeCount: 10, EpisodeFileCount: 6, Aired: 10},
				Seasons: []series.Season{
					{Number: 1, Monitored: true, Statistics: small},
				}},
			{ID: 2, Title: "Long Anime", Monitored: true,
				Statistics: series.Statistics{EpisodeCount: 500, EpisodeFileCount: 100, Aired: 500},
				Seasons: []series.Season{
					{Number: 1, Monitored: true, Statistics: huge},
				}},
			{ID: 9, Title: "Legacy", Monitored: true,
				Statistics: series.Statistics{EpisodeFileCount: 0, EpisodeCount: 5},
				Seasons: []series.Season{
					{Number: 1, Monitored: true, Statistics: legacy},
				}},
		},
		epsBySeries: map[shareddomain.SonarrSeriesID][]series.Episode{
			1: {
				{Number: 1, SeasonNumber: 1, Title: "Pilot", Monitored: true, HasFile: true, AirDateUTC: past},
				{Number: 2, SeasonNumber: 1, Title: "The Reveal", Monitored: true, HasFile: false, AirDateUTC: past},
				{Number: 3, SeasonNumber: 1, Title: "", Monitored: true, HasFile: false, AirDateUTC: past},
				{Number: 4, SeasonNumber: 1, Title: "Future Tease", Monitored: true, HasFile: false, AirDateUTC: future},
			},
			9: {
				{Number: 1, SeasonNumber: 1, Title: "Pilot", Monitored: true, HasFile: false, AirDateUTC: past},
			},
		},
	}
	w := doMissing(t, "alpha",
		map[string]ports.SonarrClient{"alpha": mf},
		map[string]string{"alpha": "auto"})
	require.Equal(t, http.StatusOK, w.Code)

	calls := mf.listEpisodesBySeriesCalls()
	// Perf invariant: one call per series with at least one embed-
	// eligible season. Series 2 (Long Anime, aired=500) is over the
	// cap → skipped. Series 1 + 9 = 2 calls total, NOT 3.
	assert.Len(t, calls, 2, "exactly one fetch per embed-eligible series")
	gotIDs := map[shareddomain.SonarrSeriesID]bool{}
	for _, id := range calls {
		gotIDs[id] = true
	}
	assert.True(t, gotIDs[1], "series 1 must be fetched")
	assert.True(t, gotIDs[9], "series 9 must be fetched")
	assert.False(t, gotIDs[2], "series 2 (over embed cap) must NOT be fetched")

	var body struct {
		Items []struct {
			SeriesID int `json:"series_id"`
			Seasons  []struct {
				SeasonNumber      int `json:"season_number"`
				MissingAiredCount int `json:"missing_aired_count"`
				AiredEpisodeCount int `json:"aired_episode_count"`
				Episodes          []struct {
					Number  int    `json:"number"`
					Title   string `json:"title"`
					Present bool   `json:"present"`
				} `json:"episodes"`
			} `json:"seasons"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Items, 3)
	byID := make(map[int]int, len(body.Items))
	for i, it := range body.Items {
		byID[it.SeriesID] = i
	}

	// Small season: embed populated, sorted, future-dated filtered.
	small1 := body.Items[byID[1]].Seasons[0]
	require.Len(t, small1.Episodes, 3, "future-dated episode must be filtered out")
	assert.Equal(t, 1, small1.Episodes[0].Number)
	assert.Equal(t, "Pilot", small1.Episodes[0].Title)
	assert.True(t, small1.Episodes[0].Present)
	assert.Equal(t, "The Reveal", small1.Episodes[1].Title)
	assert.False(t, small1.Episodes[1].Present)
	assert.Empty(t, small1.Episodes[2].Title)

	// Large season: cap exceeded → embed omitted.
	large2 := body.Items[byID[2]].Seasons[0]
	assert.Equal(t, 500, large2.AiredEpisodeCount)
	assert.Nil(t, large2.Episodes, "seasons over the embed cap omit episodes")

	// Legacy fixture (Aired=0, EpisodeCount fallback): embed populated.
	legacy9 := body.Items[byID[9]].Seasons[0]
	assert.Equal(t, 5, legacy9.AiredEpisodeCount)
	require.Len(t, legacy9.Episodes, 1)
}

// TestInstancesHandler_Missing_PartialFailureKeepsRequest200 asserts
// that a single-series upstream failure WARN-logs and DROPS the embed
// for that one series without poisoning siblings. The queue request
// MUST stay 200 — a flaky Sonarr endpoint can't be allowed to break
// the entire queue page.
func TestInstancesHandler_Missing_PartialFailureKeepsRequest200(t *testing.T) {
	past := time.Now().Add(-7 * 24 * time.Hour)
	stat := series.Statistics{EpisodeCount: 5, EpisodeFileCount: 2, Aired: 5}
	mf := &missingFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		all: []series.Series{
			{ID: 1, Title: "OK", Monitored: true, Statistics: stat,
				Seasons: []series.Season{{Number: 1, Monitored: true, Statistics: stat}}},
			{ID: 2, Title: "Boom", Monitored: true, Statistics: stat,
				Seasons: []series.Season{{Number: 1, Monitored: true, Statistics: stat}}},
		},
		epsBySeries: map[shareddomain.SonarrSeriesID][]series.Episode{
			1: {{Number: 1, SeasonNumber: 1, Title: "Pilot", HasFile: true, AirDateUTC: past}},
		},
		errBySeries: map[shareddomain.SonarrSeriesID]error{
			2: errors.New("sonarr 503"),
		},
	}
	w := doMissing(t, "alpha",
		map[string]ports.SonarrClient{"alpha": mf},
		map[string]string{"alpha": "auto"})
	require.Equal(t, http.StatusOK, w.Code,
		"single-series failure must not break the queue request")
	var body struct {
		Items []struct {
			SeriesID int `json:"series_id"`
			Seasons  []struct {
				Episodes []struct{ Number int } `json:"episodes"`
			} `json:"seasons"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Items, 2)
	byID := map[int]int{}
	for i, it := range body.Items {
		byID[it.SeriesID] = i
	}
	// Survivor row still has its embed.
	assert.Len(t, body.Items[byID[1]].Seasons[0].Episodes, 1)
	// Failed row: embed omitted, but the season/stat row still ships.
	assert.Empty(t, body.Items[byID[2]].Seasons[0].Episodes)
}

// TestInstancesHandler_Missing_CacheHitsSkipUpstream pre-populates the
// in-process LRU cache for one series and asserts the handler skips
// the upstream ListEpisodesBySeries call for that ID. The cache is
// keyed by (instance, seriesID); a different instance must still
// trigger a fetch (collision guard).
func TestInstancesHandler_Missing_CacheHitsSkipUpstream(t *testing.T) {
	past := time.Now().Add(-7 * 24 * time.Hour)
	stat := series.Statistics{EpisodeCount: 3, EpisodeFileCount: 1, Aired: 3}
	mf := &missingFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		all: []series.Series{
			{ID: 1, Title: "Cached", Monitored: true, Statistics: stat,
				Seasons: []series.Season{{Number: 1, Monitored: true, Statistics: stat}}},
			{ID: 2, Title: "Cold", Monitored: true, Statistics: stat,
				Seasons: []series.Season{{Number: 1, Monitored: true, Statistics: stat}}},
		},
		epsBySeries: map[shareddomain.SonarrSeriesID][]series.Episode{
			1: {{Number: 1, SeasonNumber: 1, Title: "Live", HasFile: true, AirDateUTC: past}},
			2: {{Number: 1, SeasonNumber: 1, Title: "Live", HasFile: false, AirDateUTC: past}},
		},
	}
	cache := sonarr.NewLRUEpisodesCache(0, time.Hour)
	// Pre-warm the cache for series 1 only; series 2 cold.
	cache.Put(sonarr.EpisodesCacheKey("alpha", 1), []series.Episode{
		{Number: 1, SeasonNumber: 1, Title: "Cached", HasFile: true, AirDateUTC: past},
	})
	w := doMissingWithEpisodesCache(t, "alpha",
		map[string]ports.SonarrClient{"alpha": mf},
		map[string]string{"alpha": "auto"}, cache)
	require.Equal(t, http.StatusOK, w.Code)

	calls := mf.listEpisodesBySeriesCalls()
	assert.Len(t, calls, 1, "only series 2 (cold) should hit upstream")
	assert.Equal(t, shareddomain.SonarrSeriesID(2), calls[0], "the one upstream call must be for series 2")

	var body struct {
		Items []struct {
			SeriesID int `json:"series_id"`
			Seasons  []struct {
				Episodes []struct {
					Title string `json:"title"`
				} `json:"episodes"`
			} `json:"seasons"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	byID := map[int]int{}
	for i, it := range body.Items {
		byID[it.SeriesID] = i
	}
	// The cached embed wins over the live fixture — proof we used the
	// cache, not the upstream call.
	assert.Equal(t, "Cached", body.Items[byID[1]].Seasons[0].Episodes[0].Title)
	assert.Equal(t, "Live", body.Items[byID[2]].Seasons[0].Episodes[0].Title)
}

// TestInstancesHandler_Missing_ConcurrentFetchBudget validates the
// errgroup fan-out. With concurrency=5 and 9 series each at 200 ms
// simulated upstream latency, sequential would be ≥ 1.8 s while
// concurrent (5 parallel slots) must finish under 1 s.
func TestInstancesHandler_Missing_ConcurrentFetchBudget(t *testing.T) {
	past := time.Now().Add(-7 * 24 * time.Hour)
	stat := series.Statistics{EpisodeCount: 5, EpisodeFileCount: 2, Aired: 5}
	all := make([]series.Series, 9)
	epsBySeries := map[shareddomain.SonarrSeriesID][]series.Episode{}
	for i := range 9 {
		id := shareddomain.SonarrSeriesID(i + 1)
		all[i] = series.Series{
			ID: id, Title: "S", Monitored: true, Statistics: stat,
			Seasons: []series.Season{{Number: 1, Monitored: true, Statistics: stat}},
		}
		epsBySeries[id] = []series.Episode{
			{Number: 1, SeasonNumber: 1, Title: "ep", HasFile: false, AirDateUTC: past},
		}
	}
	mf := &missingFakeSonarr{
		fakeSonarr:    &fakeSonarr{name: "alpha"},
		all:           all,
		epsBySeries:   epsBySeries,
		upstreamDelay: 200 * time.Millisecond,
	}
	start := time.Now()
	w := doMissing(t, "alpha",
		map[string]ports.SonarrClient{"alpha": mf},
		map[string]string{"alpha": "auto"})
	elapsed := time.Since(start)
	require.Equal(t, http.StatusOK, w.Code)

	// Sequential floor: 9 × 200 ms = 1.8 s.
	// Concurrent (limit 5) ceiling: ceil(9/5) × 200 ms = 400 ms + slack.
	assert.Less(t, elapsed, time.Second,
		"concurrent fan-out budget violated: sequential would be ≥1.8s, got %s", elapsed)
	assert.Len(t, mf.listEpisodesBySeriesCalls(), 9,
		"exactly one upstream call per series")
}

func TestInstanceDTO_EmitsMode(t *testing.T) {
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{&fakeSonarr{name: "alpha"}})
	c.Preflight(context.Background())
	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, buildRegistry(nil, map[string]string{"alpha": "manual"}, nil), nil).List)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Instances []map[string]any `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	_, hasMode := body.Instances[0]["mode"]
	assert.True(t, hasMode, "mode must always be emitted (Q-010-1)")
	assert.Equal(t, "manual", body.Instances[0]["mode"])
}

// doSearch wires a one-route gin engine and returns the recorder.
// Mirrors doMissing but for the search endpoint; takes the raw query
// string (e.g. "q=high&limit=2") so each test can hit a different
// permutation without param-builder noise.
func doSearch(t *testing.T, name, rawQuery string, clients map[string]ports.SonarrClient) *httptest.ResponseRecorder {
	t.Helper()
	c := healthcheck.New(openInstancesDB(t), nil)
	r := gin.New()
	h := NewInstancesHandler(c, buildRegistry(clients, nil, nil), nil)
	r.GET("/api/v1/instances/:name/series", h.SearchSeries)
	url := "/api/v1/instances/" + name + "/series"
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// searchFixture builds a small, deterministic series set covering the
// branches under test: mixed monitored/unmonitored, mixed titles for
// substring + sort, mixed statistics for MissingAired derivation.
func searchFixture() []series.Series {
	return []series.Series{
		{ID: 1, Title: "Severance", Monitored: true,
			Statistics: series.Statistics{EpisodeCount: 18, EpisodeFileCount: 10},
			Seasons: []series.Season{
				{Number: 1, Monitored: true}, {Number: 2, Monitored: true},
			}},
		{ID: 2, Title: "High Maintenance", Monitored: true,
			Statistics: series.Statistics{EpisodeCount: 50, EpisodeFileCount: 50},
			Seasons: []series.Season{
				{Number: 1, Monitored: true}, {Number: 2, Monitored: false},
			}},
		{ID: 3, Title: "Highlander", Monitored: false,
			Statistics: series.Statistics{EpisodeCount: 100, EpisodeFileCount: 0},
			Seasons:    []series.Season{{Number: 1, Monitored: false}}},
		{ID: 4, Title: "Andor", Monitored: true,
			Statistics: series.Statistics{EpisodeCount: 12, EpisodeFileCount: 12},
			Seasons:    []series.Season{{Number: 1, Monitored: true}}},
	}
}

type searchBody struct {
	Items []struct {
		SeriesID     int    `json:"series_id"`
		Title        string `json:"title"`
		Monitored    bool   `json:"monitored"`
		SeasonCount  int    `json:"season_count"`
		MissingAired int    `json:"missing_aired_count"`
	} `json:"items"`
	Total int `json:"total"`
}

func TestSearchSeries_OK(t *testing.T) {
	mf := &missingFakeSonarr{fakeSonarr: &fakeSonarr{name: "alpha"}, all: searchFixture()}
	w := doSearch(t, "alpha", "", map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusOK, w.Code)

	var body searchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	// Default monitored=any → all four come through; sorted by title ASC.
	require.Len(t, body.Items, 4)
	assert.Equal(t, 4, body.Total)
	titles := []string{body.Items[0].Title, body.Items[1].Title, body.Items[2].Title, body.Items[3].Title}
	assert.Equal(t, []string{"Andor", "High Maintenance", "Highlander", "Severance"}, titles)
	// Severance: 8 missing, 2 monitored seasons.
	for _, it := range body.Items {
		if it.SeriesID == 1 {
			assert.Equal(t, 8, it.MissingAired)
			assert.Equal(t, 2, it.SeasonCount)
		}
		// High Maintenance: 1 monitored season (the second is monitored=false).
		if it.SeriesID == 2 {
			assert.Equal(t, 1, it.SeasonCount)
			assert.Equal(t, 0, it.MissingAired)
		}
	}
}

func TestSearchSeries_QueryFilter(t *testing.T) {
	mf := &missingFakeSonarr{fakeSonarr: &fakeSonarr{name: "alpha"}, all: searchFixture()}
	// q=high — substring, case-insensitive → "High Maintenance" + "Highlander".
	w := doSearch(t, "alpha", "q=high", map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusOK, w.Code)

	var body searchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Items, 2)
	assert.Equal(t, 2, body.Total)
	assert.Equal(t, "High Maintenance", body.Items[0].Title)
	assert.Equal(t, "Highlander", body.Items[1].Title)
}

func TestSearchSeries_MonitoredFilter(t *testing.T) {
	mf := &missingFakeSonarr{fakeSonarr: &fakeSonarr{name: "alpha"}, all: searchFixture()}
	w := doSearch(t, "alpha", "monitored=true", map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusOK, w.Code)

	var body searchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	// Three monitored series: Andor, High Maintenance, Severance.
	// Highlander (unmonitored) excluded.
	require.Len(t, body.Items, 3)
	assert.Equal(t, 3, body.Total)
	for _, it := range body.Items {
		assert.True(t, it.Monitored, "monitored=true must exclude %s", it.Title)
		assert.NotEqual(t, "Highlander", it.Title)
	}
}

func TestSearchSeries_LimitClampAndTotal(t *testing.T) {
	mf := &missingFakeSonarr{fakeSonarr: &fakeSonarr{name: "alpha"}, all: searchFixture()}
	// limit=2 — Total stays 4 (pre-limit), Items truncated to 2.
	w := doSearch(t, "alpha", "limit=2", map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusOK, w.Code)

	var body searchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Items, 2)
	assert.Equal(t, 4, body.Total, "Total reflects pre-limit count")
	// Sort order preserved across the slice; first two are alphabetical.
	assert.Equal(t, "Andor", body.Items[0].Title)
	assert.Equal(t, "High Maintenance", body.Items[1].Title)
}

func TestSearchSeries_LimitValidation(t *testing.T) {
	mf := &missingFakeSonarr{fakeSonarr: &fakeSonarr{name: "alpha"}, all: searchFixture()}
	tests := []struct {
		name  string
		query string
	}{
		{"too high", "limit=200"},
		{"zero", "limit=0"},
		{"negative", "limit=-1"},
		{"non-int", "limit=abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doSearch(t, "alpha", tt.query, map[string]ports.SonarrClient{"alpha": mf})
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			var body map[string]string
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Contains(t, body["error"], "limit")
		})
	}
}

func TestSearchSeries_MonitoredValidation(t *testing.T) {
	mf := &missingFakeSonarr{fakeSonarr: &fakeSonarr{name: "alpha"}, all: searchFixture()}
	w := doSearch(t, "alpha", "monitored=yolo", map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "monitored")
}

func TestSearchSeries_UnknownInstance(t *testing.T) {
	w := doSearch(t, "ghost", "", map[string]ports.SonarrClient{})
	require.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "ghost")
}

func TestSearchSeries_SonarrUnauthorized(t *testing.T) {
	mf := &missingFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		err:        sharedErrors.ErrInstanceUnauthorized,
	}
	w := doSearch(t, "alpha", "", map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusBadGateway, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "unauthorized")
}

func TestList_IncludesURL(t *testing.T) {
	t.Parallel()
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{&fakeSonarr{name: "alpha"}})
	c.Preflight(context.Background())
	urls := map[string]string{"alpha": "http://sonarr.example:8989"}
	r := gin.New()
	r.GET("/api/v1/instances",
		NewInstancesHandler(c, buildRegistry(nil, nil, urls), nil).List)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body dto.InstanceList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	assert.Equal(t, "http://sonarr.example:8989", body.Instances[0].URL)
}

// TestInstancesList_DoesNotLeakAPIKey locks the contract that
// GET /instances must never include any variant of an apiKey field.
// Future careless DTO edits fail here.
func TestInstancesList_DoesNotLeakAPIKey(t *testing.T) {
	t.Parallel()
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{&fakeSonarr{name: "alpha"}})
	c.Preflight(context.Background())
	r := gin.New()
	r.GET("/api/v1/instances",
		NewInstancesHandler(c, buildRegistry(nil, map[string]string{"alpha": "auto"}, nil), nil).List)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	assert.NotContains(t, body, "api_key", "GET /instances must not include api_key")
	assert.NotContains(t, body, "apiKey", "GET /instances must not include apiKey")
	assert.NotContains(t, body, "apikey", "GET /instances must not include apikey")
}

// TestList_IncludesPublicURL asserts the list endpoint surfaces
// PublicURL when an instance has it set. F-P0-9: SPA hero card
// uses it to render the "Sonarr" link with the browser-facing URL.
func TestList_IncludesPublicURL(t *testing.T) {
	t.Parallel()
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{&fakeSonarr{name: "alpha"}})
	c.Preflight(context.Background())

	pub := "https://sonarr.example.com"
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{
				Name:      "alpha",
				URL:       "http://sonarr:8989",
				PublicURL: &pub,
			}},
		}
	}}
	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, reg, nil).List)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.InstanceList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	assert.Equal(t, "https://sonarr.example.com", body.Instances[0].PublicURL)
	assert.Equal(t, "http://sonarr:8989", body.Instances[0].URL)
}

// TestList_OmitsPublicURLWhenUnset asserts public_url is absent
// from the JSON envelope when the instance has no override
// (omitempty on the DTO). Empty *PublicURL (deref == "") is
// treated as unset to mirror UIURL() semantics.
func TestList_OmitsPublicURLWhenUnset(t *testing.T) {
	t.Parallel()
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{&fakeSonarr{name: "alpha"}})
	c.Preflight(context.Background())

	empty := ""
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{
				Name:      "alpha",
				URL:       "http://sonarr:8989",
				PublicURL: &empty,
			}},
		}
	}}
	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, reg, nil).List)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var raw struct {
		Instances []map[string]any `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	require.Len(t, raw.Instances, 1)
	_, hasKey := raw.Instances[0]["public_url"]
	assert.False(t, hasKey,
		"public_url must be omitted when unset / empty-string")
}

// buildRegistry composes a registry suitable for handler tests from the
// three legacy maps the older tests carried. Any map may be nil. Load
// returns a fresh copy each call.
func buildRegistry(clients map[string]ports.SonarrClient, modes, urls map[string]string) InstanceRegistry {
	merged := map[string]scan.Instance{}
	ensure := func(name string) {
		if _, ok := merged[name]; !ok {
			merged[name] = scan.Instance{Config: config.SonarrInstance{Name: name}}
		}
	}
	for n := range clients {
		ensure(n)
	}
	for n := range modes {
		ensure(n)
	}
	for n := range urls {
		ensure(n)
	}
	for n, c := range clients {
		inst := merged[n]
		inst.Client = c
		merged[n] = inst
	}
	for n, m := range modes {
		inst := merged[n]
		inst.Config.Mode = m
		merged[n] = inst
	}
	for n, u := range urls {
		inst := merged[n]
		inst.Config.URL = u
		merged[n] = inst
	}
	cp := merged
	return InstanceRegistry{Load: func() map[string]scan.Instance {
		out := make(map[string]scan.Instance, len(cp))
		maps.Copy(out, cp)
		return out
	}}
}

// stubSeriesCache satisfies ports.SeriesCacheRepository for the 041g
// enrichment tests. Only ListActiveByInstance is exercised by Missing.
type stubSeriesCache struct {
	entries  []series.CacheEntry
	listErr  error
	listCall int
}

func (s *stubSeriesCache) Get(_ context.Context, _ shareddomain.InstanceName, _ shareddomain.SonarrSeriesID) (series.CacheEntry, error) {
	return series.CacheEntry{}, ports.ErrNotFound
}
func (s *stubSeriesCache) Upsert(_ context.Context, _ series.CacheEntry) error { return nil }
func (s *stubSeriesCache) SoftDelete(_ context.Context, _ shareddomain.InstanceName, _ shareddomain.SonarrSeriesID) error {
	return nil
}
func (s *stubSeriesCache) ListActiveByInstance(_ context.Context, _ shareddomain.InstanceName) ([]series.CacheEntry, error) {
	s.listCall++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.entries, nil
}
func (s *stubSeriesCache) ListByFilter(_ context.Context, _ shareddomain.InstanceName, _ ports.SeriesCacheFilter, _ ports.SeriesCacheSort, _ ports.Pagination) ([]series.CacheEntry, int, bool, *ports.Cursor, error) {
	return nil, 0, false, nil, nil
}
func (s *stubSeriesCache) FetchLastGrabInfo(_ context.Context, _ shareddomain.InstanceName, _ []shareddomain.SonarrSeriesID) (map[shareddomain.SonarrSeriesID]ports.LastGrabInfo, error) {
	return make(map[shareddomain.SonarrSeriesID]ports.LastGrabInfo), nil
}
func (s *stubSeriesCache) ListDistinctNetworks(_ context.Context, _ shareddomain.InstanceName) ([]string, error) {
	return nil, nil
}
func (s *stubSeriesCache) GetInstancesBySeriesID(_ context.Context, _ shareddomain.SeriesID) ([]shareddomain.InstanceName, error) {
	return nil, nil
}
func (s *stubSeriesCache) GetInstancesBySeriesIDs(_ context.Context, _ []shareddomain.SeriesID) (map[shareddomain.SeriesID][]shareddomain.InstanceName, error) {
	return map[shareddomain.SeriesID][]shareddomain.InstanceName{}, nil
}
func (s *stubSeriesCache) ListBySeriesID(_ context.Context, _ shareddomain.SeriesID) ([]series.CacheEntry, error) {
	return nil, nil
}

var _ ports.SeriesCacheRepository = (*stubSeriesCache)(nil)

// missingFixtureTwo returns two monitored series with aired-missing > 0
// so both flow through the Missing pipeline.
func missingFixtureTwo() []series.Series {
	return []series.Series{
		{ID: 1, Title: "Severance", Monitored: true,
			Statistics: series.Statistics{EpisodeCount: 18, EpisodeFileCount: 10},
			Seasons: []series.Season{{Number: 2, Monitored: true,
				Statistics: series.Statistics{EpisodeCount: 9, EpisodeFileCount: 1}}}},
		{ID: 2, Title: "Andor", Monitored: true,
			Statistics: series.Statistics{EpisodeCount: 12, EpisodeFileCount: 6},
			Seasons: []series.Season{{Number: 1, Monitored: true,
				Statistics: series.Statistics{EpisodeCount: 12, EpisodeFileCount: 6}}}},
	}
}

// enrichedItem captures the 041g fields plus series_id for ordering.
// 348b: PosterHash captured to assert the cache-join propagation.
// Story 350 dropped the legacy poster_path field.
type enrichedItem struct {
	SeriesID   int     `json:"series_id"`
	TitleSlug  string  `json:"title_slug"`
	Year       *int    `json:"year,omitempty"`
	PosterHash *string `json:"poster_hash,omitempty"`
}

func decodeEnrichedItems(t *testing.T, raw []byte) []enrichedItem {
	t.Helper()
	var body struct {
		Items []enrichedItem `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	return body.Items
}

// TestInstancesHandler_Missing_CacheJoin covers the four 041g paths in
// one table: full-hit (AC-1, AC-3), miss (AC-2), partial (AC-2), and
// repo-error (AC-4). All run against the same missingFixtureTwo so the
// shape assertions stay uniform.
func TestInstancesHandler_Missing_CacheJoin(t *testing.T) {
	entryOne := series.CacheEntry{InstanceName: "alpha", SonarrSeriesID: 1,
		TitleSlug: "severance", Year: new(2022)}
	entryTwo := series.CacheEntry{InstanceName: "alpha", SonarrSeriesID: 2,
		TitleSlug: "andor", Year: new(2022)}

	tests := []struct {
		name    string
		entries []series.CacheEntry
		listErr error
		assert  func(t *testing.T, items []enrichedItem, cache *stubSeriesCache)
	}{
		{
			name:    "full_cache_hit",
			entries: []series.CacheEntry{entryOne, entryTwo},
			assert: func(t *testing.T, items []enrichedItem, cache *stubSeriesCache) {
				require.Len(t, items, 2)
				assert.Equal(t, "severance", items[0].TitleSlug)
				require.NotNil(t, items[0].Year)
				assert.Equal(t, 2022, *items[0].Year)
				assert.Equal(t, "andor", items[1].TitleSlug)
				// AC-3: single batch lookup, not N+1.
				assert.Equal(t, 1, cache.listCall)
			},
		},
		{
			name:    "all_cache_miss",
			entries: nil,
			assert: func(t *testing.T, items []enrichedItem, _ *stubSeriesCache) {
				require.Len(t, items, 2)
				for _, it := range items {
					assert.Equal(t, "", it.TitleSlug)
					assert.Nil(t, it.Year)
					assert.Nil(t, it.PosterHash)
				}
			},
		},
		{
			name:    "partial_cache_coverage",
			entries: []series.CacheEntry{entryTwo}, // only id=2 cached.
			assert: func(t *testing.T, items []enrichedItem, _ *stubSeriesCache) {
				require.Len(t, items, 2)
				assert.Equal(t, 1, items[0].SeriesID)
				assert.Equal(t, "", items[0].TitleSlug)
				assert.Nil(t, items[0].Year)
				assert.Equal(t, 2, items[1].SeriesID)
				assert.Equal(t, "andor", items[1].TitleSlug)
				require.NotNil(t, items[1].Year)
			},
		},
		{
			name:    "repo_error_does_not_fail",
			listErr: errors.New("db down"),
			assert: func(t *testing.T, items []enrichedItem, _ *stubSeriesCache) {
				require.Len(t, items, 2)
				for _, it := range items {
					assert.Equal(t, "", it.TitleSlug)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mf := &missingFakeSonarr{fakeSonarr: &fakeSonarr{name: "alpha"}, all: missingFixtureTwo()}
			cache := &stubSeriesCache{entries: tc.entries, listErr: tc.listErr}
			w := doMissingWithCache(t, "alpha",
				map[string]ports.SonarrClient{"alpha": mf},
				map[string]string{"alpha": "manual"}, cache)
			require.Equal(t, http.StatusOK, w.Code, "cache failures must not bubble into the HTTP response")
			tc.assert(t, decodeEnrichedItems(t, w.Body.Bytes()), cache)
		})
	}
}

// When the series_cache entry carries a non-nil PosterAsset (raw canon
// path), the /missing wire response carries a deterministic
// `poster_hash` derived from the synthetic CDN URL. Entries without a
// path omit the field. No extra SQL — the existing ListActiveByInstance
// call is reused (cache.listCall == 1).
func TestInstancesHandler_Missing_PosterHashField(t *testing.T) {
	path := "/poster-test.jpg"
	expectedHash := appmedia.HashFromURL(appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, path))
	entryOne := series.CacheEntry{InstanceName: "alpha", SonarrSeriesID: 1,
		TitleSlug: "severance", Year: new(2022),
		PosterAsset: new(path)}
	// Series 2 has no canon path — proves omitempty + nil propagation.
	entryTwo := series.CacheEntry{InstanceName: "alpha", SonarrSeriesID: 2,
		TitleSlug: "andor", Year: new(2022)}

	mf := &missingFakeSonarr{fakeSonarr: &fakeSonarr{name: "alpha"}, all: missingFixtureTwo()}
	cache := &stubSeriesCache{entries: []series.CacheEntry{entryOne, entryTwo}}
	w := doMissingWithCache(t, "alpha",
		map[string]ports.SonarrClient{"alpha": mf},
		map[string]string{"alpha": "manual"}, cache)
	require.Equal(t, http.StatusOK, w.Code)

	items := decodeEnrichedItems(t, w.Body.Bytes())
	require.Len(t, items, 2)

	// Series 1 — hash derived from canon path.
	assert.Equal(t, 1, items[0].SeriesID)
	require.NotNil(t, items[0].PosterHash, "derived hash must propagate to wire")
	assert.Equal(t, expectedHash, *items[0].PosterHash)

	// Series 2 — no canon path, field omitted from JSON entirely.
	assert.Equal(t, 2, items[1].SeriesID)
	assert.Nil(t, items[1].PosterHash, "nil path must omit the wire field")
	assert.NotContains(t, w.Body.String(), `"poster_hash":null`,
		"omitempty must drop the key entirely, not emit null")

	// AC: no extra SQL — single ListActiveByInstance call covers both
	// title_slug AND poster_hash enrichment.
	assert.Equal(t, 1, cache.listCall, "poster_hash must reuse the slug join — no fanout")
}
