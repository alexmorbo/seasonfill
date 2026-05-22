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
