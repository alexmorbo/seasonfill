package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		GUIDRewrites: append([]runtime.GUIDRewriteRule(nil), s.GUIDRewrites...),
		UpdatedAt:    time.Now().UTC(),
	}
	f.exists = true
	return nil
}
func (f *rcFakeRuntime) SaveAPIKey(_ context.Context, _ []byte, _ bool) error { return nil }
func (f *rcFakeRuntime) UpsertOIDCSecret(_ context.Context, _ string) error   { return nil }
func (f *rcFakeRuntime) DecryptOIDCSecret(_ context.Context) (string, error)  { return "", nil }

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
		"dry_run":           true,
		"global_rate_limit": map[string]any{"rpm": 30, "burst": 10},
		"auth": map[string]any{
			"session_ttl":     "12h",
			"secure_cookie":   false,
			"trusted_proxies": []string{"127.0.0.1", "::1"},
			"mode":            "forms",
			"local_bypass":    false,
			"local_networks":  []string{"127.0.0.0/8", "10.0.0.0/8"},
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
	storedAt := time.Now().UTC().Add(time.Hour)
	repo.mu.Lock()
	repo.row.UpdatedAt = storedAt
	repo.mu.Unlock()
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", validRCBody(),
		map[string]string{"If-Unmodified-Since": time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat)})
	assert.Equal(t, http.StatusPreconditionFailed, w.Code)
	assert.Contains(t, w.Body.String(), "STALE_WRITE")
	// B-5: 412 must carry the current Last-Modified so the SPA can
	// retry without an extra GET.
	lm := w.Header().Get("Last-Modified")
	require.NotEmpty(t, lm, "412 must include Last-Modified for client retry")
	parsed, err := http.ParseTime(lm)
	require.NoError(t, err, "Last-Modified must parse as RFC1123: %q", lm)
	assert.True(t, parsed.Equal(storedAt.Truncate(time.Second)),
		"Last-Modified must match the current stored row's updated_at")
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

// --- Part 028i: uncovered branches ---

// TestRC_Get_ErrorPath exercises the Get handler when the usecase returns
// an unexpected error (triggers the default branch in writeError).
func TestRC_Get_ErrorPath(t *testing.T) {
	t.Parallel()
	uc := runtimeconfig.New(&rcBrokenRuntime{}, rcFakeInstances{}, nil, runtime.NewBus(nil), slog.Default())
	h := NewRuntimeConfigHandler(uc, slog.Default())
	r := gin.New()
	r.GET("/api/v1/config/runtime", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/config/runtime", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// rcBrokenRuntime always returns an internal error from Get.
type rcBrokenRuntime struct{}

func (rcBrokenRuntime) Get(_ context.Context) (ports.RuntimeConfigRow, error) {
	return ports.RuntimeConfigRow{}, errors.New("db exploded")
}
func (rcBrokenRuntime) Upsert(_ context.Context, _ runtime.Snapshot, _ *time.Time) error {
	return nil
}
func (rcBrokenRuntime) SaveAPIKey(_ context.Context, _ []byte, _ bool) error { return nil }
func (rcBrokenRuntime) UpsertOIDCSecret(_ context.Context, _ string) error   { return nil }
func (rcBrokenRuntime) DecryptOIDCSecret(_ context.Context) (string, error)  { return "", nil }

// TestRC_WriteError_DefaultBranch exercises the default (internal error) branch
// of writeError directly via the Update handler path.
func TestRC_WriteError_DefaultBranch(t *testing.T) {
	t.Parallel()
	uc := runtimeconfig.New(&rcBrokenUpsert{}, rcFakeInstances{}, nil, runtime.NewBus(nil), slog.Default())
	h := NewRuntimeConfigHandler(uc, slog.Default())
	r := gin.New()
	r.PUT("/api/v1/config/runtime", h.Update)

	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", validRCBody(), nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

type rcBrokenUpsert struct{ rcFakeRuntime }

func (r *rcBrokenUpsert) Get(_ context.Context) (ports.RuntimeConfigRow, error) {
	return ports.RuntimeConfigRow{}, nil
}
func (r *rcBrokenUpsert) Upsert(_ context.Context, _ runtime.Snapshot, _ *time.Time) error {
	return errors.New("upsert exploded")
}

// TestRC_Body_WrongContentType exercises the content-type guard in
// readRuntimeConfigBody (the first early-return, not yet covered).
func TestRC_Body_WrongContentType(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/api/v1/config/runtime", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
}

// TestRC_Body_MalformedJSON exercises the json.Unmarshal failure branch in
// readRuntimeConfigBody — not covered by existing tests.
func TestRC_Body_MalformedJSON(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/api/v1/config/runtime", bytes.NewReader([]byte("{bad")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "malformed body")
}

// TestRC_IUS_BadFormat exercises the http.ParseTime failure branch in Update
// when the If-Unmodified-Since header is unparseable.
func TestRC_IUS_BadFormat(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", validRCBody(),
		map[string]string{"If-Unmodified-Since": "not-a-date"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "If-Unmodified-Since")
}

// --- 107: guid_rewrites DTO round trip -----------------------------------

func TestRC_Get_GUIDRewrites_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	w := rcDoJSON(t, r, http.MethodGet, "/api/v1/config/runtime", nil, nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var body struct {
		GUIDRewrites json.RawMessage `json:"guid_rewrites"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "[]", string(body.GUIDRewrites),
		"GET with no row must emit guid_rewrites as `[]` not `null`")
}

func TestRC_Put_GUIDRewrites_RoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	b := validRCBody()
	b["guid_rewrites"] = []map[string]any{
		{"from": "http://rutracker-proxy", "to": "https://rutracker.org"},
		{"from": "http://nnm-proxy", "to": "https://nnm-club.me"},
	}
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", b, nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	rules := got["guid_rewrites"].([]any)
	require.Len(t, rules, 2)
	first := rules[0].(map[string]any)
	assert.Equal(t, "http://rutracker-proxy", first["from"])
	assert.Equal(t, "https://rutracker.org", first["to"])
	second := rules[1].(map[string]any)
	assert.Equal(t, "http://nnm-proxy", second["from"])
	assert.Equal(t, "https://nnm-club.me", second["to"])

	// Round trip via GET — order must be preserved.
	w2 := rcDoJSON(t, r, http.MethodGet, "/api/v1/config/runtime", nil, nil)
	require.Equal(t, http.StatusOK, w2.Code)
	var got2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &got2))
	rules2 := got2["guid_rewrites"].([]any)
	require.Len(t, rules2, 2)
	first2 := rules2[0].(map[string]any)
	assert.Equal(t, "http://rutracker-proxy", first2["from"])
}

func TestRC_Put_GUIDRewrites_DuplicateFrom_400(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	b := validRCBody()
	b["guid_rewrites"] = []map[string]any{
		{"from": "http://a", "to": "https://x"},
		{"from": "http://a", "to": "https://y"},
	}
	w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", b, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_GUID_REWRITE_DUPLICATE_FROM")
}

// TestRC_Body_TooLarge exercises the MaxBytesError branch in
// readRuntimeConfigBody by sending a body larger than runtimeConfigBodyLimit (64 KiB).
func TestRC_Body_TooLarge(t *testing.T) {
	t.Parallel()
	r, _ := setupRC(t)
	// Send (runtimeConfigBodyLimit + 1) bytes with JSON content-type.
	body := bytes.Repeat([]byte("x"), runtimeConfigBodyLimit+1)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/api/v1/config/runtime", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
}
