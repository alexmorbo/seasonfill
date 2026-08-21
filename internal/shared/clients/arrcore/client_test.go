package arrcore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/admin/infrastructure/ratelimit"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

func TestClient_SystemStatus(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"3.0.10","instanceName":"http://sonarr.local"}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	st, err := c.SystemStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "/api/v3/system/status", gotPath)
	assert.Equal(t, "secret", gotKey)
	assert.Equal(t, "3.0.10", st.Version)
	assert.Equal(t, "http://sonarr.local", st.InstanceURL)
}

func TestClient_GetQualityProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/qualityprofile/14", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":14,"name":"HD-1080p","items":[
				{"allowed":true,"quality":{"id":9,"name":"HDTV-1080p"}},
				{"allowed":false,"quality":{"id":3,"name":"SDTV"}},
				{"allowed":false,"name":"WEB 1080p","items":[
					{"allowed":true,"quality":{"id":15,"name":"WEBDL-1080p"}}
				]}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	prof, err := c.GetQualityProfile(context.Background(), 14)
	require.NoError(t, err)
	assert.Equal(t, 14, prof.ID)
	assert.Equal(t, "HD-1080p", prof.Name)
	require.Len(t, prof.Items, 2, "only allowed items (top-level + nested) surface")
	assert.Equal(t, 9, prof.Items[0].ID)
	assert.Equal(t, 15, prof.Items[1].ID)
}

func TestClient_ListQualityProfiles_Success(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"name":"Any"},{"id":7,"name":"HD-1080p"}]`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	profs, err := c.ListQualityProfiles(context.Background())
	require.NoError(t, err)
	require.Len(t, profs, 2)
	assert.Equal(t, "/api/v3/qualityprofile", gotPath, "must hit LIST endpoint, not /{id}")
	assert.Equal(t, "secret", gotKey)
	assert.Equal(t, 1, profs[0].ID)
	assert.Equal(t, "Any", profs[0].Name)
	assert.Equal(t, 7, profs[1].ID)
	assert.Equal(t, "HD-1080p", profs[1].Name)
}

func TestClient_ListQualityProfiles_5xxIsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	_, err := c.ListQualityProfiles(context.Background())
	require.Error(t, err)
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusServiceUnavailable, se.Status)
}

func TestClient_ListQualityProfiles_401WrapsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "bad", 5*time.Second, nil)
	_, err := c.ListQualityProfiles(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedErrors.ErrInstanceUnauthorized))
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusUnauthorized, se.Status)
}

func TestClient_ListRootFolders_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":1,"path":"/tv","accessible":true,"freeSpace":1099511627776},
			{"id":2,"path":"/anime","accessible":false,"freeSpace":0}
		]`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	roots, err := c.ListRootFolders(context.Background())
	require.NoError(t, err)
	require.Len(t, roots, 2)
	assert.Equal(t, "/api/v3/rootfolder", gotPath)
	assert.Equal(t, 1, roots[0].ID)
	assert.Equal(t, "/tv", roots[0].Path)
	assert.True(t, roots[0].Accessible)
	assert.Equal(t, int64(1099511627776), roots[0].FreeSpace)
	assert.False(t, roots[1].Accessible)
	assert.Equal(t, int64(0), roots[1].FreeSpace)
}

func TestClient_CreateTag_Success(t *testing.T) {
	var (
		mu       sync.Mutex
		gotPath  string
		gotMeth  string
		gotKey   string
		gotCType string
		gotBody  createTagRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body) //nolint:bodyclose // httptest
		mu.Lock()
		_ = json.Unmarshal(body, &gotBody)
		gotPath = r.URL.Path
		gotMeth = r.Method
		gotKey = r.Header.Get("X-Api-Key")
		gotCType = r.Header.Get("Content-Type")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"label":"sf-alice"}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	tag, err := c.CreateTag(context.Background(), "sf-alice")
	require.NoError(t, err)
	assert.Equal(t, 42, tag.ID)
	assert.Equal(t, "sf-alice", tag.Label)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "/api/v3/tag", gotPath)
	assert.Equal(t, http.MethodPost, gotMeth)
	assert.Equal(t, "secret", gotKey)
	assert.Equal(t, "application/json", gotCType)
	assert.Equal(t, "sf-alice", gotBody.Label)
}

func TestClient_ListTags_Success(t *testing.T) {
	var (
		mu      sync.Mutex
		gotPath string
		gotMeth string
		gotKey  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		gotMeth = r.Method
		gotKey = r.Header.Get("X-Api-Key")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"label":"sf-alice"},{"id":7,"label":"sf-system"}]`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	tags, err := c.ListTags(context.Background())
	require.NoError(t, err)
	require.Len(t, tags, 2)
	assert.Equal(t, 1, tags[0].ID)
	assert.Equal(t, "sf-alice", tags[0].Label)
	assert.Equal(t, 7, tags[1].ID)
	assert.Equal(t, "sf-system", tags[1].Label)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "/api/v3/tag", gotPath)
	assert.Equal(t, http.MethodGet, gotMeth)
	assert.Equal(t, "secret", gotKey)
}

// TestClient_ListTags_Empty — an arr with no tags returns [] (not null); the
// resolver must fall through to CreateTag rather than nil-panic.
func TestClient_ListTags_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	tags, err := c.ListTags(context.Background())
	require.NoError(t, err)
	assert.Empty(t, tags)
}

// --- transport error mapping (vehicle = SystemStatus) ---

func TestClient_Unauthorized_WrapsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := New("t", srv.URL, "bad", 2*time.Second, nil)
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedErrors.ErrInstanceUnauthorized))
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusUnauthorized, se.Status)
}

func TestClient_Forbidden_WrapsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := New("t", srv.URL, "bad", 2*time.Second, nil)
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedErrors.ErrInstanceUnauthorized))
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusForbidden, se.Status)
}

func TestClient_NetworkError_WrapsSentinel(t *testing.T) {
	c := New("t", "http://127.0.0.1:1", "k", 200*time.Millisecond, nil)
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedErrors.ErrInstanceNetwork))
}

func TestClient_CtxCancelMidRequestReturnsCtxErrNotNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		time.Sleep(10 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.SystemStatus(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
	assert.False(t, errors.Is(err, sharedErrors.ErrInstanceNetwork))
}

func TestClient_GlobalLimiterConsulted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"x"}`))
	}))
	t.Cleanup(srv.Close)

	global := ratelimit.New(1, 1)
	c := New("test", srv.URL, "k", 5*time.Second, nil, WithGlobalLimiter(global))

	_, err := c.SystemStatus(context.Background())
	require.NoError(t, err)

	start := time.Now()
	_, err = c.SystemStatus(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 500*time.Millisecond)
}

func TestClient_NilLimitersAreNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"x"}`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "k", 2*time.Second, nil, WithGlobalLimiter(nil))
	for range 5 {
		_, err := c.SystemStatus(context.Background())
		require.NoError(t, err)
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

	c := New("test", srv.URL, "k", 5*time.Second, nil, WithGlobalLimiter(global))
	_, err := c.SystemStatus(context.Background())
	require.NoError(t, err)
	_, err = c.SystemStatus(context.Background())
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, calls)
	assert.Equal(t, []string{"global"}, scopes)
}

func TestClient_WithGlobalLimiterPointer_LiveReload(t *testing.T) {
	t.Parallel()
	var ptr atomic.Pointer[ratelimit.Limiter]
	ptr.Store(nil)

	c := New("alpha", "http://invalid.test", "k", time.Millisecond, nil,
		WithGlobalLimiterPointer(&ptr))
	require.NotNil(t, c)
	assert.Nil(t, c.globalLimiter())

	lim := ratelimit.NewFromRPM(1, 1)
	ptr.Store(lim)
	assert.Same(t, lim, c.globalLimiter())
}

// TestClient_StatusError_DefaultArrIsSonarr verifies the zero-value arr path:
// a client built WITHOUT WithArrName surfaces StatusError text beginning
// "sonarr " — byte-identical to pre-Ф6-R-3 (errtext/grab tests depend on this).
func TestClient_StatusError_DefaultArrIsSonarr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("oops"))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil)
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err)
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, "sonarr", se.Arr, "New() defaults arr to sonarr")
	assert.Equal(t,
		"sonarr /api/v3/system/status returned status=500 body=oops",
		se.Error(),
	)
}

// TestClient_StatusError_RadarrArrName verifies WithArrName("radarr") stamps
// the StatusError so its text begins "radarr " — the whole point of the R-3
// parameterization (radarr errors must not lie and say "sonarr").
func TestClient_StatusError_RadarrArrName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 5*time.Second, nil, WithArrName("radarr"))
	_, err := c.SystemStatus(context.Background())
	require.Error(t, err)
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, "radarr", se.Arr)
	assert.Equal(t,
		"radarr /api/v3/system/status returned status=500 body=boom",
		se.Error(),
	)
}

// TestClient_SearchGet_UsesSearchTimeout directly covers the moved httpSearch
// path: base timeout 50ms, search timeout 1s, server sleeps 200ms — succeeds.
func TestClient_SearchGet_UsesSearchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	c := New("test", srv.URL, "secret", 50*time.Millisecond, nil,
		WithSearchTimeout(1*time.Second))
	var out []int
	err := c.SearchGet(context.Background(), "/api/v3/release", nil, &out)
	require.NoError(t, err, "search timeout must be honoured")
}
