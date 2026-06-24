package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

type fakeLookup struct {
	name   string
	client ports.SonarrClient
}

func (f fakeLookup) Lookup(name string) (ports.SonarrClient, bool) {
	if name != f.name {
		return nil, false
	}
	return f.client, true
}

type fakeUsers struct{}

func (fakeUsers) GetCurrent(_ context.Context, _ string) (*admin.User, error) {
	return &admin.User{ID: 1, Username: "alex"}, nil
}

type fakeCache struct {
	upsertedCount int
}

func (f *fakeCache) Get(_ context.Context, _ uint, _ domain.InstanceName) (admin.UserInstanceTag, error) {
	return admin.UserInstanceTag{}, ports.ErrNotFound
}

func (f *fakeCache) Upsert(_ context.Context, _ admin.UserInstanceTag) error {
	f.upsertedCount++
	return nil
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func buildRouter(t *testing.T, client ports.SonarrClient, lookupName string) *gin.Engine {
	t.Helper()
	log := discardLog()
	resolver := discoapp.NewTagResolver(&fakeCache{}, log)
	uc := discoapp.NewAddToSonarrUseCase(
		fakeLookup{name: lookupName, client: client},
		fakeUsers{},
		resolver,
		log,
	)
	handler := NewAddToSonarrHandler(uc, log)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(log))
	r.POST("/api/v1/discovery/add-to-sonarr", handler.Handle)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/discovery/add-to-sonarr", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestAddToSonarrHandler_HappyPath_200(t *testing.T) {
	t.Parallel()
	// No middleware mounts UsernameContextKey → handler runs the bypass
	// path and expects "sf-system" label. Tag exists already so no
	// CreateTag is needed; AddSeries returns the new id.
	client := &ports.SonarrClientMock{
		ListTagsFunc: func(_ context.Context) ([]ports.Tag, error) {
			return []ports.Tag{{ID: 7, Label: "sf-system"}}, nil
		},
		CreateTagFunc: func(_ context.Context, label string) (ports.Tag, error) {
			return ports.Tag{ID: 99, Label: label}, nil
		},
		AddSeriesFunc: func(_ context.Context, _ ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			return ports.AddSeriesResult{SonarrSeriesID: 555}, nil
		},
	}
	r := buildRouter(t, client, "main")

	w, out := doJSON(t, r, map[string]any{
		"instance_name":      "main",
		"tvdb_id":            81189,
		"quality_profile_id": 6,
		"root_folder_path":   "/tv",
		"monitor_mode":       "all",
	})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, float64(555), out["sonarr_series_id"])
	assert.Equal(t, "main", out["instance_name"])
	assert.Equal(t, "sf-system", out["user_tag_label"])
}

func TestAddToSonarrHandler_InstanceUnknown_404(t *testing.T) {
	t.Parallel()
	client := &ports.SonarrClientMock{}
	r := buildRouter(t, client, "main")

	w, out := doJSON(t, r, map[string]any{
		"instance_name":      "ghost",
		"tvdb_id":            1,
		"quality_profile_id": 1,
		"root_folder_path":   "/tv",
	})
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "instance_not_found", out["error"])
}

func TestAddToSonarrHandler_BadBody_400(t *testing.T) {
	t.Parallel()
	client := &ports.SonarrClientMock{}
	r := buildRouter(t, client, "main")

	cases := []struct {
		name string
		body any
	}{
		{"missing_instance_name", map[string]any{
			"tvdb_id": 1, "quality_profile_id": 1, "root_folder_path": "/tv",
		}},
		{"zero_tvdb_id", map[string]any{
			"instance_name": "main", "tvdb_id": 0,
			"quality_profile_id": 1, "root_folder_path": "/tv",
		}},
		{"missing_root_folder", map[string]any{
			"instance_name": "main", "tvdb_id": 1, "quality_profile_id": 1,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, out := doJSON(t, r, tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, tc.name)
			assert.Equal(t, "invalid_request", out["error"], tc.name)
		})
	}
}

func TestAddToSonarrHandler_SonarrUnreachable_502(t *testing.T) {
	t.Parallel()
	client := &ports.SonarrClientMock{
		ListTagsFunc: func(_ context.Context) ([]ports.Tag, error) {
			return []ports.Tag{{ID: 7, Label: "sf-system"}}, nil
		},
		CreateTagFunc: func(_ context.Context, label string) (ports.Tag, error) {
			return ports.Tag{ID: 99, Label: label}, nil
		},
		AddSeriesFunc: func(_ context.Context, _ ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			return ports.AddSeriesResult{}, errors.New("dial tcp: refused")
		},
	}
	r := buildRouter(t, client, "main")

	w, out := doJSON(t, r, map[string]any{
		"instance_name":      "main",
		"tvdb_id":            1,
		"quality_profile_id": 1,
		"root_folder_path":   "/tv",
	})
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Equal(t, "sonarr_unreachable", out["error"])
}
