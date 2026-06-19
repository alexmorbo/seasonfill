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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func newTestServer(t *testing.T, routes map[string]string) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	for path, fixture := range routes {
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
	dlID, err := c.ForceGrab(context.Background(), "abc", 3)
	require.NoError(t, err)
	assert.Equal(t, "", dlID, "empty body should yield empty downloadID")
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
	dlID, err := c.ForceGrab(context.Background(), "abc", 3)
	require.Error(t, err)
	assert.Equal(t, "", dlID)
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
	dlID, err := c.ForceGrab(context.Background(), "abc", 3)
	require.Error(t, err)
	assert.Equal(t, "", dlID)
	assert.True(t, IsTransient(err), "429 should be transient (H-3)")
}

func TestClient_ForceGrab_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	dlID, err := c.ForceGrab(context.Background(), "abc", 3)
	require.Error(t, err)
	assert.Equal(t, "", dlID)
	assert.True(t, IsTransient(err))
	assert.False(t, Is4xx(err))
}

func TestClient_ForceGrab_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 50*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	dlID, err := c.ForceGrab(context.Background(), "abc", 3)
	require.Error(t, err)
	assert.Equal(t, "", dlID)
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

	for range 5 {
		_, err := c.SystemStatus(context.Background())
		require.NoError(t, err)
	}
}

func TestClient_ForceGrab_ReturnsDownloadClientID_WhenPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"guid":"abc","indexerId":3,"downloadClientId":4242}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	dlID, err := c.ForceGrab(context.Background(), "abc", 3)
	require.NoError(t, err)
	assert.Equal(t, "4242", dlID)
}

func TestClient_ForceGrab_ReturnsEmpty_WhenDownloadClientIDAbsent(t *testing.T) {
	cases := map[string]string{
		"absent_key":       `{"guid":"abc"}`,
		"null_value":       `{"downloadClientId":null}`,
		"zero_value":       `{"downloadClientId":0}`,
		"empty_object":     `{}`,
		"non_json_skipped": `not json — decode error is non-fatal`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(srv.Close)

			c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			dlID, err := c.ForceGrab(context.Background(), "abc", 3)
			require.NoError(t, err)
			assert.Equal(t, "", dlID, "case %s should yield empty downloadID", name)
		})
	}
}

func TestClient_GlobalLimiterObserverFiresOnBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"x"}`))
	}))
	t.Cleanup(srv.Close)

	var (
		mu     sync.Mutex
		calls  int
		scopes []string
	)
	global := ratelimit.NewWithOptions(5, 1, ratelimit.WithObserver("global", func(s string) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		scopes = append(scopes, s)
	}))
	require.NotNil(t, global)

	c := NewWithOptions("test", srv.URL, "k", 5*time.Second, nil,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WithGlobalLimiter(global))

	// First call drains the burst — no observer fire.
	_, err := c.SystemStatus(context.Background())
	require.NoError(t, err)
	// Second call must wait ~200 ms — observer fires.
	_, err = c.SystemStatus(context.Background())
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, calls, "observer should fire exactly once for the blocked call")
	assert.Equal(t, []string{"global"}, scopes)
}

// TestClient_SearchReleases_UsesSearchTimeout asserts SearchReleases
// honours the long-timeout client when WithSearchTimeout is wired up.
// Strategy: base timeout = 50ms, search timeout = 1s, server sleeps
// 200ms on /api/v3/release — must succeed.
func TestClient_SearchReleases_UsesSearchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	c := NewWithOptions("test", srv.URL, "secret",
		50*time.Millisecond, // base timeout — would time out at 200ms
		nil,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WithSearchTimeout(1*time.Second), // search timeout — generous
	)
	rels, err := c.SearchReleases(context.Background(), 1, 1)
	require.NoError(t, err, "search timeout must be honoured")
	assert.Empty(t, rels)
}

// TestClient_SearchReleases_FallsBackToBaseTimeoutWhenSearchUnset
// asserts that without WithSearchTimeout, SearchReleases keeps using
// the base client's timeout. Guards the alias default.
func TestClient_SearchReleases_FallsBackToBaseTimeoutWhenSearchUnset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 50*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := c.SearchReleases(context.Background(), 1, 1)
	require.Error(t, err, "without WithSearchTimeout, base timeout applies to search")
	assert.True(t, IsTransient(err), "client-side timeout maps to transient")
}

// TestClient_OtherEndpoints_UseBaseTimeoutNotSearchTimeout asserts
// that ListSeries / SystemStatus / etc. keep the base timeout even
// when a longer search timeout is installed. This is the critical
// non-regression check — a misrouted method would gain unexpected
// slack on health checks.
func TestClient_OtherEndpoints_UseBaseTimeoutNotSearchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c := NewWithOptions("test", srv.URL, "secret",
		50*time.Millisecond, // base timeout — must fire
		nil,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WithSearchTimeout(5*time.Second), // long search timeout — must NOT apply here
	)
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err, "SystemStatus must use base timeout, not search timeout")
	assert.True(t, IsTransient(err))
}

// TestClient_WithSearchTimeout_ZeroIsNoOp asserts the defensive
// guard: WithSearchTimeout(0) leaves httpSearch aliasing http.
func TestClient_WithSearchTimeout_ZeroIsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c := NewWithOptions("test", srv.URL, "secret",
		50*time.Millisecond,
		nil,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WithSearchTimeout(0), // explicitly zero — must be no-op
	)
	_, err := c.SearchReleases(context.Background(), 1, 1)
	require.Error(t, err,
		"WithSearchTimeout(0) must be a no-op — base 50ms timeout applies")
	assert.True(t, IsTransient(err))
}

// TestClient_WithSearchTimeout_NegativeIsNoOp — same as above but
// negative. Defensive against operators passing a sentinel like -1
// to mean "unset".
func TestClient_WithSearchTimeout_NegativeIsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c := NewWithOptions("test", srv.URL, "secret",
		50*time.Millisecond,
		nil,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WithSearchTimeout(-1*time.Second),
	)
	_, err := c.SearchReleases(context.Background(), 1, 1)
	require.Error(t, err, "WithSearchTimeout(<0) must be a no-op")
	assert.True(t, IsTransient(err))
}

// TestClient_SearchReleases_ContextDeadlineWinsOverSearchTimeout
// asserts that a caller-supplied ctx deadline still bounds the
// search even when WithSearchTimeout is generous. Guards against a
// future bug where the long-timeout client somehow strips the ctx
// deadline.
func TestClient_SearchReleases_ContextDeadlineWinsOverSearchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	c := NewWithOptions("test", srv.URL, "secret",
		5*time.Second,
		nil,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WithSearchTimeout(30*time.Second), // very generous
	)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := c.SearchReleases(ctx, 1, 1)
	require.Error(t, err)
	// ctx-cancel must NOT be wrapped as network error (matches the
	// pre-015 invariant from TestClient_CtxCancelMidRequestReturnsCtxErrNotNetwork).
}

func TestSonarrClient_WithGlobalLimiterPointer_LiveReload(t *testing.T) {
	t.Parallel()
	var ptr atomic.Pointer[ratelimit.Limiter]
	// Start unlimited.
	ptr.Store(nil)

	c := NewWithOptions("alpha", "http://invalid.test", "k",
		time.Millisecond, nil, slog.Default(),
		WithGlobalLimiterPointer(&ptr))
	require.NotNil(t, c)
	// Calling globalLimiter on nil pointer must not panic.
	assert.Nil(t, c.globalLimiter())

	// Swap in a limiter, confirm live read sees it.
	lim := ratelimit.NewFromRPM(1, 1)
	ptr.Store(lim)
	assert.Same(t, lim, c.globalLimiter())
}

func TestClient_ListEpisodeFilesBySeason_HappyPath(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/episodeFile", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "122", r.URL.Query().Get("seriesId"))
		// Sonarr ignores seasonNumber on this endpoint; client drops it.
		require.Empty(t, r.URL.Query().Get("seasonNumber"))
		_, _ = w.Write([]byte(`[
			{"id": 7001, "seriesId": 122, "seasonNumber": 2,
			 "relativePath": "Season 02/Severance.S02E01.mkv",
			 "size": 13325829734,
			 "quality": {"quality": {"id": 19, "name": "WEBDL-2160p"}}},
			{"id": 7002, "seriesId": 122, "seasonNumber": 2,
			 "relativePath": "Season 02/Severance.S02E02.mkv",
			 "size": 12100000000,
			 "quality": {"quality": {"id": 19, "name": "WEBDL-2160p"}}}
		]`))
	})
	mux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "2", r.URL.Query().Get("seasonNumber"))
		_, _ = w.Write([]byte(`[
			{"id": 1, "episodeNumber": 1, "seasonNumber": 2, "episodeFileId": 7001},
			{"id": 2, "episodeNumber": 2, "seasonNumber": 2, "episodeFileId": 7002}
		]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	out, err := c.ListEpisodeFilesBySeason(context.Background(), 122, 2)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, 7001, out[0].ID)
	assert.Equal(t, "Season 02/Severance.S02E01.mkv", out[0].RelativePath)
	assert.Equal(t, int64(13325829734), out[0].SizeBytes)
	assert.Equal(t, "WEBDL-2160p", out[0].Quality)
	assert.Equal(t, []int{1}, out[0].EpisodeNumbers)
	assert.Equal(t, []int{2}, out[1].EpisodeNumbers)
}

func TestClient_ListEpisodeFilesBySeason_MultiEpFile(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/episodeFile", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": 7003, "seasonNumber": 2,
			"relativePath": "Season 02/Severance.S02E03-E04.mkv",
			"size": 25000000000,
			"quality": {"quality": {"id": 19, "name": "WEBDL-2160p"}}}]`))
	})
	mux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id": 3, "episodeNumber": 3, "seasonNumber": 2, "episodeFileId": 7003},
			{"id": 4, "episodeNumber": 4, "seasonNumber": 2, "episodeFileId": 7003}
		]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	out, err := c.ListEpisodeFilesBySeason(context.Background(), 122, 2)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, []int{3, 4}, out[0].EpisodeNumbers, "multi-ep file groups numbers")
}

// TestClient_ListEpisodeFilesBySeason_UnmappedFileHasEmptyEpisodes ensures that
// an episodeFile row with no corresponding episode entries surfaces as a
// non-nil empty slice, and that the eventual JSON encoding emits "[]" instead
// of "null". Sonarr can legitimately return such rows (orphaned imports,
// stale rescans); the frontend GrabDrawer crashed reading `.length` on
// `null` before this guard. 043c regression for the EpisodeFilesList crash.
func TestClient_ListEpisodeFilesBySeason_UnmappedFileHasEmptyEpisodes(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/episodeFile", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": 7099, "seasonNumber": 2,
			"relativePath": "Season 02/Severance.S02E99.orphan.mkv",
			"size": 1000000,
			"quality": {"quality": {"id": 19, "name": "WEBDL-2160p"}}}]`))
	})
	mux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	out, err := c.ListEpisodeFilesBySeason(context.Background(), 122, 2)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.NotNil(t, out[0].EpisodeNumbers, "unmapped file must surface non-nil EpisodeNumbers")
	assert.Empty(t, out[0].EpisodeNumbers, "unmapped file must surface empty EpisodeNumbers")

	// JSON-marshal directly — the field has no struct tag at the ports
	// layer, but the wire format is `[]` because the slice is non-nil.
	// This guards against a future refactor reintroducing the nil leak.
	raw, err := json.Marshal(out[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"EpisodeNumbers":[]`,
		"non-nil empty slice must marshal as [], never null")
	assert.NotContains(t, string(raw), `"EpisodeNumbers":null`,
		"nil leak would regress GrabDrawer crash on /grabs?open=<id>")
}

// TestClient_ListEpisodeFilesBySeason_FiltersCrossSeason guards the
// operator-#4 Rick&Morty bug: Sonarr's /api/v3/episodeFile endpoint
// ignores `seasonNumber` and returns every file for the series — the
// client must filter to the requested season in Go.
func TestClient_ListEpisodeFilesBySeason_FiltersCrossSeason(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/episodeFile", func(w http.ResponseWriter, _ *http.Request) {
		// Sonarr returns ALL seasons regardless of seasonNumber query.
		_, _ = w.Write([]byte(`[
			{"id": 1, "seasonNumber": 1, "relativePath": "S01/E01.mkv",
			 "quality": {"quality": {"id": 19, "name": "WEBDL-1080p"}}},
			{"id": 2, "seasonNumber": 2, "relativePath": "S02/E01.mkv",
			 "quality": {"quality": {"id": 19, "name": "WEBDL-1080p"}}},
			{"id": 3, "seasonNumber": 5, "relativePath": "S05/E01.mkv",
			 "quality": {"quality": {"id": 19, "name": "WEBDL-1080p"}}},
			{"id": 4, "seasonNumber": 9, "relativePath": "S09/E01.mkv",
			 "quality": {"quality": {"id": 19, "name": "WEBDL-2160p"}}},
			{"id": 5, "seasonNumber": 9, "relativePath": "S09/E02.mkv",
			 "quality": {"quality": {"id": 19, "name": "WEBDL-2160p"}}},
			{"id": 6, "seasonNumber": 9, "relativePath": "S09/E03.mkv",
			 "quality": {"quality": {"id": 19, "name": "WEBDL-2160p"}}}
		]`))
	})
	mux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id": 90, "episodeNumber": 1, "seasonNumber": 9, "episodeFileId": 4},
			{"id": 91, "episodeNumber": 2, "seasonNumber": 9, "episodeFileId": 5},
			{"id": 92, "episodeNumber": 3, "seasonNumber": 9, "episodeFileId": 6}
		]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	out, err := c.ListEpisodeFilesBySeason(context.Background(), 555, 9)
	require.NoError(t, err)
	require.Len(t, out, 3, "must return only S09 entries — Sonarr's seasonNumber filter is a no-op")
	for _, ef := range out {
		assert.Equal(t, 9, ef.SeasonNumber)
	}
	ids := []int{out[0].ID, out[1].ID, out[2].ID}
	assert.ElementsMatch(t, []int{4, 5, 6}, ids)
}

// TestClient_QueueAll asserts QueueAll calls /api/v3/queue without
// the seriesId filter and decodes downloadId verbatim.
func TestClient_QueueAll(t *testing.T) {
	t.Parallel()
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{
			"page": 1, "pageSize": 1000, "totalRecords": 2,
			"records": [
				{"id": 1, "seriesId": 11, "downloadId": "ABCDEF",
				 "title": "Show.S01.PACK", "status": "downloading",
				 "protocol": "torrent", "seasonNumber": 1, "episodeId": 100},
				{"id": 2, "seriesId": 22, "downloadId": "abcabc",
				 "title": "Other", "status": "queued",
				 "protocol": "torrent", "seasonNumber": 2, "episodeId": 200}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	payload, err := c.QueueAll(context.Background())
	require.NoError(t, err)
	require.Len(t, payload.Records, 2)
	assert.NotContains(t, gotQuery, "seriesId", "QueueAll must NOT send seriesId filter")
	assert.Contains(t, gotQuery, "pageSize=1000")
	assert.Contains(t, gotQuery, "includeSeries=false")
	assert.Contains(t, gotQuery, "includeEpisode=false")
	assert.Equal(t, "ABCDEF", payload.Records[0].DownloadID, "queue returns downloadId verbatim")
	assert.Equal(t, shareddomain.SonarrSeriesID(11), payload.Records[0].SeriesID)
	assert.Equal(t, shareddomain.SonarrSeriesID(22), payload.Records[1].SeriesID)
	assert.Equal(t, 2, payload.TotalRecords)
}

// TestClient_GrabHistoryPaged_PageNumbersAndCap asserts the query
// string the client emits.
func TestClient_GrabHistoryPaged_PageNumbersAndCap(t *testing.T) {
	t.Parallel()
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{
			"page": 7, "pageSize": 50, "totalRecords": 1000,
			"records": [{"eventType": "grabbed", "downloadId": "deadbeef",
				"seriesId": 12, "episode": {"seasonNumber": 5}}]
		}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	hp, err := c.GrabHistoryPaged(context.Background(), 7, 50)
	require.NoError(t, err)
	assert.Contains(t, gotQuery, "page=7")
	assert.Contains(t, gotQuery, "pageSize=50")
	assert.Contains(t, gotQuery, "eventType=1")
	assert.Contains(t, gotQuery, "sortKey=date")
	assert.Contains(t, gotQuery, "sortDirection=descending")
	require.Len(t, hp.Records, 1)
	assert.Equal(t, "deadbeef", hp.Records[0].DownloadID, "downloadId stays lowercase")
	assert.Equal(t, shareddomain.SonarrSeriesID(12), hp.Records[0].SeriesID)
	assert.Equal(t, 5, hp.Records[0].SeasonNumber)
	assert.Equal(t, 7, hp.Page)
	assert.Equal(t, 50, hp.PageSize)
	assert.Equal(t, 1000, hp.TotalRecords)
}

// TestClient_GrabHistoryPaged_SkipsUsenetGrabs asserts records with
// empty downloadId are filtered out.
func TestClient_GrabHistoryPaged_SkipsUsenetGrabs(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"page": 1, "pageSize": 50, "totalRecords": 3,
			"records": [
				{"eventType": "grabbed", "downloadId": "AAAA", "seriesId": 1},
				{"eventType": "grabbed", "downloadId": "", "seriesId": 2},
				{"eventType": "grabbed", "downloadId": "BBBB", "seriesId": 3}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	hp, err := c.GrabHistoryPaged(context.Background(), 1, 50)
	require.NoError(t, err)
	require.Len(t, hp.Records, 2, "usenet (empty downloadId) rows dropped")
	assert.Equal(t, "aaaa", hp.Records[0].DownloadID)
	assert.Equal(t, "bbbb", hp.Records[1].DownloadID)
}

// TestClient_GrabHistoryPaged_DefaultsForZeroArgs asserts the client
// coerces page<=0 to 1 and pageSize<=0 to 50.
func TestClient_GrabHistoryPaged_DefaultsForZeroArgs(t *testing.T) {
	t.Parallel()
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"page":1,"pageSize":50,"totalRecords":0,"records":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := c.GrabHistoryPaged(context.Background(), 0, 0)
	require.NoError(t, err)
	assert.Contains(t, gotQuery, "page=1")
	assert.Contains(t, gotQuery, "pageSize=50")
}

func TestSeriesDTOToCacheEntry_CapturesPreviousAiring(t *testing.T) {
	t.Parallel()
	aired := time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC)
	d := seriesDTO{
		ID: 42, Title: "Sample", TitleSlug: "sample",
		PreviousAiring: &aired,
	}
	entry := seriesDTOToCacheEntry(d, "homelab")
	require.NotNil(t, entry.LastAiredAt)
	assert.True(t, entry.LastAiredAt.Equal(aired))
}

func TestSeriesDTOToCacheEntry_OmitsPreviousAiringWhenAbsent(t *testing.T) {
	t.Parallel()
	d := seriesDTO{ID: 42, Title: "Sample", TitleSlug: "sample"}
	entry := seriesDTOToCacheEntry(d, "homelab")
	assert.Nil(t, entry.LastAiredAt)
}

// TestSeriesDTOToCacheEntry_AiredFallbackToEpisodeCount — story 380.
// Sonarr's /api/v3/series LIST endpoint omits airedEpisodeCount from the
// series-level statistics block (only per-season blocks include it).
// episodeCount on the LIST response carries the same semantic, so the
// writer must fall back when airedEpisodeCount is zero.
func TestSeriesDTOToCacheEntry_AiredFallbackToEpisodeCount(t *testing.T) {
	t.Parallel()
	d := seriesDTO{
		ID: 42, Title: "Sample", TitleSlug: "sample",
		Statistics: &statisticsDTO{
			EpisodeCount:     38,
			EpisodeFileCount: 38,
			SizeOnDisk:       42_000_000_000,
			// AiredEpisodeCount intentionally absent — mirrors live LIST shape.
		},
	}
	entry := seriesDTOToCacheEntry(d, "homelab")
	assert.Equal(t, 38, entry.AiredEpisodeCount, "fallback should pick up episodeCount when airedEpisodeCount is missing")
	assert.Equal(t, 38, entry.EpisodeFileCount)
	assert.Equal(t, int64(42_000_000_000), entry.SizeOnDiskBytes)
}

// TestSeriesDTOToCacheEntry_AiredPrefersExplicit — defensive: if Sonarr
// ever starts emitting airedEpisodeCount at the series level (or fixture
// callers set both), the explicit value wins.
func TestSeriesDTOToCacheEntry_AiredPrefersExplicit(t *testing.T) {
	t.Parallel()
	d := seriesDTO{
		ID: 42, Title: "Sample", TitleSlug: "sample",
		Statistics: &statisticsDTO{
			EpisodeCount:      40,
			AiredEpisodeCount: 38,
			EpisodeFileCount:  38,
		},
	}
	entry := seriesDTOToCacheEntry(d, "homelab")
	assert.Equal(t, 38, entry.AiredEpisodeCount, "explicit airedEpisodeCount wins over episodeCount fallback")
}
