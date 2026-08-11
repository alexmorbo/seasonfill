package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// fakeRadarrHolder is a local RadarrConfigLookup for the List test — returns a
// fixed radarr instance map. Ф6-R-6b Gap 2a.
type fakeRadarrHolder struct {
	m map[string]scan.RadarrInstance
}

func (f fakeRadarrHolder) Load() map[string]scan.RadarrInstance { return f.m }

// TestInstancesHandler_List_IncludesRadarrType proves a radarr instance —
// health-checked through the widened checker seam and resolved via the radarr
// holder — appears in GET /admin/instances with type="radarr", its url, and a
// real health state; while a sonarr instance carries type="sonarr". Ф6-R-6b Gap 2a.
func TestInstancesHandler_List_IncludesRadarrType(t *testing.T) {
	c := healthcheck.New(openInstancesDB(t), nil)
	// Feed BOTH a sonarr and a radarr probe through the widened ReplaceClients
	// seam (the fanout does exactly this in production), then preflight so both
	// names land in Snapshot() with a health state.
	c.ReplaceClients(
		[]ports.ArrHealthProbe{
			&fakeSonarr{name: "tv"},
			&fakeSonarr{name: "movies"},
		},
		[]string{"tv", "movies"},
	)
	c.Preflight(context.Background())

	radarr := fakeRadarrHolder{m: map[string]scan.RadarrInstance{
		"movies": {Config: runtime.InstanceSnapshot{
			Name: "movies", Type: scan.InstanceTypeRadarr, URL: "http://radarr:7878",
		}},
	}}

	h := NewInstancesHandler(c, InstanceRegistry{}, nil).WithRadarrHolder(radarr)
	r := gin.New()
	r.GET("/api/v1/instances", h.List)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Instances []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			URL    string `json:"url"`
			Health string `json:"health"`
		} `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 2)

	byName := map[string]struct {
		Type, URL, Health string
	}{}
	for _, i := range body.Instances {
		byName[i.Name] = struct{ Type, URL, Health string }{i.Type, i.URL, i.Health}
	}

	// Radarr row: type + url resolved from the holder; health present.
	rr, ok := byName["movies"]
	require.True(t, ok, "radarr instance 'movies' must appear in the list")
	assert.Equal(t, scan.InstanceTypeRadarr, rr.Type)
	assert.Equal(t, "http://radarr:7878", rr.URL)
	assert.NotEmpty(t, rr.Health, "radarr instance must have a health state, not NULL")

	// Sonarr row: type defaults to sonarr (byte-compatible with FE `type ?? 'sonarr'`).
	sr, ok := byName["tv"]
	require.True(t, ok)
	assert.Equal(t, scan.InstanceTypeSonarr, sr.Type)
}

// TestInstancesHandler_List_NilRadarrHolder_SonarrOnly proves that without a
// radarr holder the list is sonarr-only and every row is type="sonarr" — the
// pre-Ф6-R-6b behavior is preserved.
func TestInstancesHandler_List_NilRadarrHolder_SonarrOnly(t *testing.T) {
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{&fakeSonarr{name: "tv"}})
	c.Preflight(context.Background())

	h := NewInstancesHandler(c, InstanceRegistry{}, nil) // no WithRadarrHolder
	r := gin.New()
	r.GET("/api/v1/instances", h.List)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Instances []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	assert.Equal(t, scan.InstanceTypeSonarr, body.Instances[0].Type)
}
