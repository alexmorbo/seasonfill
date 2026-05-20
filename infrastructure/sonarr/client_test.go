package sonarr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
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

func TestClient_UnauthorizedMappedToDomainSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := New("t", srv.URL, "bad", 2*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInstanceUnauthorized))
	assert.True(t, IsAuth(err))
	var se *StatusError
	assert.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusUnauthorized, se.Status)
}

func TestClient_ForbiddenMappedToDomainSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := New("t", srv.URL, "bad", 2*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInstanceUnauthorized))
	assert.True(t, IsAuth(err))
}

func TestClient_NetworkErrorMappedToDomainSentinel(t *testing.T) {
	c := New("t", "http://127.0.0.1:1", "k", 200*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInstanceNetwork))
}

func TestClient_ForceGrab_Success(t *testing.T) {
	var (
		mu       sync.Mutex
		gotBody  forceGrabRequest
		gotPath  string
		gotKey   string
		gotCType string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(body, &gotBody)
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-Api-Key")
		gotCType = r.Header.Get("Content-Type")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	err := c.ForceGrab(context.Background(), "abc", 3)
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "/api/v3/release", gotPath)
	assert.Equal(t, "abc", gotBody.GUID)
	assert.Equal(t, 3, gotBody.IndexerID)
	assert.Equal(t, "secret", gotKey)
	assert.Equal(t, "application/json", gotCType)
}

func TestClient_ForceGrab_4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad guid"}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	err := c.ForceGrab(context.Background(), "abc", 3)
	require.Error(t, err)
	assert.True(t, Is4xx(err))
	assert.False(t, IsTransient(err))
	assert.False(t, IsAuth(err))
}

func TestClient_ForceGrab_429IsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	err := c.ForceGrab(context.Background(), "abc", 3)
	require.Error(t, err)
	assert.True(t, IsTransient(err), "429 should be transient (H-3)")
}

func TestClient_ForceGrab_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	err := c.ForceGrab(context.Background(), "abc", 3)
	require.Error(t, err)
	assert.True(t, IsTransient(err))
	assert.False(t, Is4xx(err))
}

func TestClient_ForceGrab_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 50*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	err := c.ForceGrab(context.Background(), "abc", 3)
	require.Error(t, err)
	assert.True(t, IsTransient(err))
}

func TestClient_GlobalLimiterConsulted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"x"}`))
	}))
	t.Cleanup(srv.Close)

	global := ratelimit.New(1, 1)
	c := NewWithOptions("test", srv.URL, "k", 5*time.Second, nil,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WithGlobalLimiter(global))

	_, err := c.SystemStatus(context.Background())
	require.NoError(t, err)

	start := time.Now()
	_, err = c.SystemStatus(context.Background())
	require.NoError(t, err)
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 500*time.Millisecond, "global limiter should delay the second call")
}

func TestClient_CtxCancelMidRequestReturnsCtxErrNotNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// block until the client's context is cancelled
		<-r.Context().Done()
		time.Sleep(10 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.SystemStatus(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected context.DeadlineExceeded, got: %v", err)
	assert.False(t, errors.Is(err, domain.ErrInstanceNetwork), "ctx cancel must not be wrapped as network error")
}

func TestClient_NilLimitersAreNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"x"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewWithOptions("test", srv.URL, "k", 2*time.Second, nil,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WithGlobalLimiter(nil))

	for i := 0; i < 5; i++ {
		_, err := c.SystemStatus(context.Background())
		require.NoError(t, err)
	}
}
