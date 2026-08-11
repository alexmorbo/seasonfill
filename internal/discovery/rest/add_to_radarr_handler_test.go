package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/arrcore"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

type fakeRadarrLookup struct {
	name   string
	client ports.RadarrClient
}

func (f fakeRadarrLookup) Lookup(name string) (ports.RadarrClient, bool) {
	if name != f.name {
		return nil, false
	}
	return f.client, true
}

func buildRadarrRouter(t *testing.T, client ports.RadarrClient, lookupName string) *gin.Engine {
	t.Helper()
	log := discardLog()
	uc := discoapp.NewAddToRadarrUseCase(fakeRadarrLookup{name: lookupName, client: client}, log)
	handler := NewAddToRadarrHandler(uc, log)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(log))
	r.POST("/api/v1/discovery/add-to-radarr", handler.Handle)
	return r
}

func doRadarrJSON(t *testing.T, r *gin.Engine, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/discovery/add-to-radarr", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestAddToRadarrHandler_HappyPath_200(t *testing.T) {
	t.Parallel()
	client := &ports.RadarrClientMock{
		LookupMovieFunc: func(_ context.Context, _ string) ([]ports.RadarrLookupResult, error) {
			return []ports.RadarrLookupResult{{Title: "X", TitleSlug: "x", Year: 2020}}, nil
		},
		AddMovieFunc: func(_ context.Context, _ ports.AddMoviePayload) (ports.AddMovieResult, error) {
			return ports.AddMovieResult{RadarrMovieID: 777}, nil
		},
	}
	r := buildRadarrRouter(t, client, "main")

	w, out := doRadarrJSON(t, r, map[string]any{
		"instance_name":      "main",
		"tmdb_id":            603,
		"quality_profile_id": 6,
		"root_folder_path":   "/movies",
	})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, float64(777), out["radarr_movie_id"])
	assert.Equal(t, "main", out["instance_name"])
	assert.Equal(t, false, out["already_added"])
}

func TestAddToRadarrHandler_AlreadyAdded_200(t *testing.T) {
	t.Parallel()
	client := &ports.RadarrClientMock{
		LookupMovieFunc: func(_ context.Context, _ string) ([]ports.RadarrLookupResult, error) {
			return []ports.RadarrLookupResult{{Title: "X", TitleSlug: "x", Year: 2020}}, nil
		},
		AddMovieFunc: func(_ context.Context, _ ports.AddMoviePayload) (ports.AddMovieResult, error) {
			return ports.AddMovieResult{}, &arrcore.StatusError{Status: 400, Body: "MovieExistsValidator"}
		},
	}
	r := buildRadarrRouter(t, client, "main")

	w, out := doRadarrJSON(t, r, map[string]any{
		"instance_name":      "main",
		"tmdb_id":            603,
		"quality_profile_id": 6,
		"root_folder_path":   "/movies",
	})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, true, out["already_added"])
}

func TestAddToRadarrHandler_InstanceUnknown_404(t *testing.T) {
	t.Parallel()
	client := &ports.RadarrClientMock{}
	r := buildRadarrRouter(t, client, "main")

	w, out := doRadarrJSON(t, r, map[string]any{
		"instance_name":      "ghost",
		"tmdb_id":            1,
		"quality_profile_id": 1,
		"root_folder_path":   "/movies",
	})
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "instance_not_found", out["error"])
}

func TestAddToRadarrHandler_BadBody_400(t *testing.T) {
	t.Parallel()
	client := &ports.RadarrClientMock{}
	r := buildRadarrRouter(t, client, "main")

	cases := []struct {
		name string
		body any
	}{
		{"missing_instance_name", map[string]any{
			"tmdb_id": 1, "quality_profile_id": 1, "root_folder_path": "/movies",
		}},
		{"zero_tmdb_id", map[string]any{
			"instance_name": "main", "tmdb_id": 0,
			"quality_profile_id": 1, "root_folder_path": "/movies",
		}},
		{"missing_root_folder", map[string]any{
			"instance_name": "main", "tmdb_id": 1, "quality_profile_id": 1,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, out := doRadarrJSON(t, r, tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, tc.name)
			assert.Equal(t, "invalid_request", out["error"], tc.name)
		})
	}
}

func TestAddToRadarrHandler_RadarrUnreachable_502(t *testing.T) {
	t.Parallel()
	client := &ports.RadarrClientMock{
		LookupMovieFunc: func(_ context.Context, _ string) ([]ports.RadarrLookupResult, error) {
			return nil, errors.New("dial tcp: refused")
		},
	}
	r := buildRadarrRouter(t, client, "main")

	w, out := doRadarrJSON(t, r, map[string]any{
		"instance_name":      "main",
		"tmdb_id":            1,
		"quality_profile_id": 1,
		"root_folder_path":   "/movies",
	})
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Equal(t, "radarr_unreachable", out["error"])
}
