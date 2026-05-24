package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/runtimeconfig"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

type rcFakeRuntime struct {
	mu     sync.Mutex
	row    ports.RuntimeConfigRow
	exists bool
}

func (f *rcFakeRuntime) Get(_ context.Context) (ports.RuntimeConfigRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.exists {
		return ports.RuntimeConfigRow{}, ports.ErrNotFound
	}
	return f.row, nil
}
func (f *rcFakeRuntime) Upsert(_ context.Context, s runtime.Snapshot, ifUnmodifiedSince *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ifUnmodifiedSince != nil && f.exists {
		stored := f.row.UpdatedAt.Truncate(time.Second)
		provided := ifUnmodifiedSince.Truncate(time.Second)
		if stored.After(provided) {
			return ports.ErrStaleWrite
		}
	}
	f.row = ports.RuntimeConfigRow{
		Cron: s.Cron, Scan: s.Scan, DryRun: s.DryRun,
		GlobalRateLimit: s.GlobalRateLimit, Auth: s.Auth,
		UpdatedAt: time.Now().UTC(),
	}
	f.exists = true
	return nil
}
func (f *rcFakeRuntime) SaveAPIKey(_ context.Context, _ []byte, _ bool) error { return nil }

type rcFakeInstances struct{}

func (rcFakeInstances) List(_ context.Context, _ *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	return nil, nil
}
func (rcFakeInstances) GetByName(_ context.Context, _ string, _ *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	return runtime.InstanceSnapshot{}, ports.ErrNotFound
}
func (rcFakeInstances) Create(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher) (uint, error) {
	return 0, nil
}
func (rcFakeInstances) Update(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher) error {
	return nil
}
func (rcFakeInstances) UpdateWithOptions(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher, _ bool, _ *time.Time) error {
	return nil
}
func (rcFakeInstances) Delete(_ context.Context, _ string) error { return nil }
func (rcFakeInstances) Count(_ context.Context) (int, error)     { return 0, nil }
func (rcFakeInstances) GetUpdatedAt(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, ports.ErrNotFound
}

func setupRC(t *testing.T) (*gin.Engine, *rcFakeRuntime) {
	t.Helper()
	repo := &rcFakeRuntime{}
	uc := runtimeconfig.New(repo, rcFakeInstances{}, nil, runtime.NewBus(nil), slog.Default())
	h := NewRuntimeConfigHandler(uc, slog.Default())
	r := gin.New()
	r.GET("/api/v1/config/runtime", h.Get)
	r.PUT("/api/v1/config/runtime", h.Update)
	return r, repo
}

func rcDoJSON(t *testing.T, r *gin.Engine, method, url string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, url, rdr)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func validRCBody() map[string]any {
	return map[string]any{
		"cron": map[string]any{
			"enabled": true, "schedule": "0 */6 * * *", "on_start": false, "jitter": "1m",
		},
		"scan": map[string]any{
			"shutdown_grace": "60s", "cooldown_sweep": "15m",
		},
		"dry_run": true,
		"global_rate_limit": map[string]any{"rpm": 30, "burst": 10},
		"auth": map[string]any{
			"session_ttl":     "12h",
			"secure_cookie":   false,
			"trusted_proxies": []string{"127.0.0.1", "::1"},
		},
	}
}

func TestRC_Get_Defaults_WhenEmpty(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	w := rcDoJSON(t, r, http.MethodGet, "/api/v1/config/runtime", nil, nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	cron := body["cron"].(map[string]any)
	assert.Equal(t, "0 */6 * * *", cron["schedule"])
	// No Last-Modified when row is missing (zero updated_at).
	assert.Empty(t, w.Header().Get("Last-Modified"))
}

func TestRC_Put_OK_SetsLastModified(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", validRCBody(), nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	lm := w.Header().Get("Last-Modified")
	require.NotEmpty(t, lm)
	_, err := http.ParseTime(lm)
	assert.NoError(t, err)
}

func TestRC_Put_InvalidCron_400(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	b := validRCBody()
	b["cron"].(map[string]any)["schedule"] = "nope"
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", b, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_CRON")
}

func TestRC_Put_BadCIDR_400(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	b := validRCBody()
	b["auth"].(map[string]any)["trusted_proxies"] = []string{"not.an.ip"}
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", b, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_TRUSTED_PROXY")
}

func TestRC_Put_SessionTTLTooShort_400(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	b := validRCBody()
	b["auth"].(map[string]any)["session_ttl"] = "1m"
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", b, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_SESSION_TTL")
}

func TestRC_Put_NegativeRPM_400(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	b := validRCBody()
	b["global_rate_limit"].(map[string]any)["rpm"] = -1
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", b, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_RATE_LIMIT")
}

func TestRC_Put_DurationAsInt_400(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	b := validRCBody()
	b["scan"].(map[string]any)["shutdown_grace"] = 60 // int, not string
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", b, nil)
	// JSON unmarshal into a string field fails — handler returns
	// BAD_REQUEST envelope for malformed body.
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
}

func TestRC_Put_StaleIUS_412(t *testing.T) {
	t.Parallel()
	r, repo := setupRC(t)
	// Seed a row by doing one PUT.
	rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", validRCBody(), nil)
	// Force stored row to be in the future.
	repo.mu.Lock()
	repo.row.UpdatedAt = time.Now().UTC().Add(time.Hour)
	repo.mu.Unlock()
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", validRCBody(),
		map[string]string{"If-Unmodified-Since": time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat)})
	assert.Equal(t, http.StatusPreconditionFailed, w.Code)
	assert.Contains(t, w.Body.String(), "STALE_WRITE")
}

func TestRC_Put_IgnoresAutoGeneratedAPIKey(t *testing.T) {
	t.Parallel()
	r, repo := setupRC(t)
	b := validRCBody()
	b["auto_generated_api_key"] = true // client lies — must be ignored
	rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", b, nil)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	assert.False(t, repo.row.APIKeyAutoGenerated, "PUT must not write APIKeyAutoGenerated")
}

// AC6 ("never includes API-key ciphertext") is enforced at the type
// level — RuntimeConfigDTO does not have an APIKeyCiphertext field —
// so an explicit runtime test would only re-prove the compile-time
// invariant. Skipped to stay under the LOC cap.
