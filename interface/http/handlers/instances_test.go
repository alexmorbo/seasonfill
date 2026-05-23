package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
)

func TestInstancesHandler_List_AfterPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Use the same scaffolding as health_test.go — one checker, two clients,
	// one of which errors.
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{
		&fakeSonarr{name: "ok"},
		&fakeSonarr{name: "broken", err: errors.New("nope")},
	})
	c.Preflight(context.Background())

	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, nil, nil, nil).List)

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
	gin.SetMode(gin.TestMode)
	c := healthcheck.New(openInstancesDB(t), nil)
	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, nil, nil, nil).List)

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
	gin.SetMode(gin.TestMode)

	// Register one instance but do NOT call Preflight — LastCheckAt stays zero.
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{
		&fakeSonarr{name: "unchecked"},
	})

	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, nil, nil, nil).List)

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
}

func (m *missingFakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.all, nil
}

// doMissing wires a one-route gin engine and returns the recorder.
func doMissing(t *testing.T, name string, clients map[string]ports.SonarrClient, modes map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c := healthcheck.New(openInstancesDB(t), nil)
	r := gin.New()
	h := NewInstancesHandler(c, clients, modes, nil)
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

func TestInstanceDTO_EmitsMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{&fakeSonarr{name: "alpha"}})
	c.Preflight(context.Background())
	r := gin.New()
	r.GET("/api/v1/instances", NewInstancesHandler(c, nil, map[string]string{"alpha": "manual"}, nil).List)

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
	gin.SetMode(gin.TestMode)
	c := healthcheck.New(openInstancesDB(t), nil)
	r := gin.New()
	h := NewInstancesHandler(c, clients, nil, nil)
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
			Seasons: []series.Season{{Number: 1, Monitored: false}}},
		{ID: 4, Title: "Andor", Monitored: true,
			Statistics: series.Statistics{EpisodeCount: 12, EpisodeFileCount: 12},
			Seasons: []series.Season{{Number: 1, Monitored: true}}},
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
		tt := tt
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
		err:        domain.ErrInstanceUnauthorized,
	}
	w := doSearch(t, "alpha", "", map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusBadGateway, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "unauthorized")
}
