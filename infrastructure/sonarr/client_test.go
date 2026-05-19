package sonarr

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T, routes map[string]string) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	for path, fixture := range routes {
		path := path
		fixture := fixture
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Api-Key") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			data, err := os.ReadFile(filepath.Join("fixtures", fixture)) //nolint:gosec // test
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		})
	}
	srv := httptest.NewServer(mux)
	client := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	return srv, client
}

func TestClient_SystemStatus(t *testing.T) {
	_, c := newTestServer(t, map[string]string{
		"/api/v3/system/status": "system-status.json",
	})
	st, err := c.SystemStatus(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, st.Version)
}

func TestClient_ListEpisodes(t *testing.T) {
	_, c := newTestServer(t, map[string]string{
		"/api/v3/episode": "episodes-s122-s2.json",
	})
	eps, err := c.ListEpisodes(context.Background(), 122, 2)
	require.NoError(t, err)
	require.NotEmpty(t, eps)
	assert.Equal(t, 2, eps[0].SeasonNumber)
}

func TestClient_SearchReleases(t *testing.T) {
	_, c := newTestServer(t, map[string]string{
		"/api/v3/release": "releases-s122-s2.json",
	})
	rels, err := c.SearchReleases(context.Background(), 122, 2)
	require.NoError(t, err)
	require.NotEmpty(t, rels)
	assert.Equal(t, "rt-1", rels[0].GUID)
	assert.Equal(t, 500, rels[0].CustomFormatScore)
}

func TestClient_GetQualityProfile(t *testing.T) {
	_, c := newTestServer(t, map[string]string{
		"/api/v3/qualityprofile/14": "qualityprofile-14.json",
	})
	prof, err := c.GetQualityProfile(context.Background(), 14)
	require.NoError(t, err)
	assert.Equal(t, 14, prof.ID)
	require.NotEmpty(t, prof.Items)
}

func TestClient_ListIndexers(t *testing.T) {
	_, c := newTestServer(t, map[string]string{
		"/api/v3/indexer": "indexer-list.json",
	})
	idx, err := c.ListIndexers(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, idx)
}

func TestClient_GrabHistory(t *testing.T) {
	_, c := newTestServer(t, map[string]string{
		"/api/v3/history": "history-s122-grabbed.json",
	})
	hist, err := c.GrabHistory(context.Background(), 122)
	require.NoError(t, err)
	require.NotEmpty(t, hist)
}

func TestClient_UnauthorizedWhenMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := New("t", srv.URL, "", 2*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err)
}
