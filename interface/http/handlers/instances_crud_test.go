package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/instance"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// crudFakeRepo is a minimal in-process implementation of
// ports.SonarrInstanceRepository. We can't reuse the one in
// application/instance/usecase_test.go (different package) so this
// is a deliberate duplication kept tight to handler-test needs.
type crudFakeRepo struct {
	rows    map[string]runtime.InstanceSnapshot
	updated map[string]time.Time
	count   int
	nextID  uint
}

func newCRUDFakeRepo() *crudFakeRepo {
	return &crudFakeRepo{
		rows:    map[string]runtime.InstanceSnapshot{},
		updated: map[string]time.Time{}, nextID: 1,
	}
}
func (f *crudFakeRepo) List(_ context.Context, _ *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	out := make([]runtime.InstanceSnapshot, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}
func (f *crudFakeRepo) GetByName(_ context.Context, name string, _ *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	r, ok := f.rows[name]
	if !ok {
		return runtime.InstanceSnapshot{}, ports.ErrNotFound
	}
	return r, nil
}
func (f *crudFakeRepo) Create(_ context.Context, inst runtime.InstanceSnapshot, _ *crypto.Cipher) (uint, error) {
	inst.ID = f.nextID
	f.nextID++
	f.rows[inst.Name] = inst
	f.updated[inst.Name] = time.Now().UTC()
	f.count++
	return inst.ID, nil
}
func (f *crudFakeRepo) UpdateWithOptions(_ context.Context, inst runtime.InstanceSnapshot, _ *crypto.Cipher, _ bool, ifUnmodifiedSince *time.Time) error {
	if ifUnmodifiedSince != nil {
		stored, ok := f.updated[inst.Name]
		if ok {
			s := stored.Truncate(time.Second)
			p := ifUnmodifiedSince.Truncate(time.Second)
			if s.After(p) {
				return ports.ErrStaleWrite
			}
		}
	}
	f.rows[inst.Name] = inst
	f.updated[inst.Name] = time.Now().UTC()
	return nil
}
func (f *crudFakeRepo) Delete(_ context.Context, name string) error {
	if _, ok := f.rows[name]; !ok {
		return ports.ErrNotFound
	}
	delete(f.rows, name)
	delete(f.updated, name)
	f.count--
	return nil
}
func (f *crudFakeRepo) Count(_ context.Context) (int, error) { return f.count, nil }
func (f *crudFakeRepo) GetUpdatedAt(_ context.Context, name string) (time.Time, error) {
	ts, ok := f.updated[name]
	if !ok {
		return time.Time{}, ports.ErrNotFound
	}
	return ts, nil
}

type crudFakeRuntime struct{}

func (crudFakeRuntime) Get(_ context.Context) (ports.RuntimeConfigRow, error) {
	return ports.RuntimeConfigRow{}, nil
}
func (crudFakeRuntime) Upsert(_ context.Context, _ runtime.Snapshot, _ *time.Time) error {
	return nil
}
func (crudFakeRuntime) SaveAPIKey(_ context.Context, _ []byte, _ bool) error { return nil }
func (crudFakeRuntime) UpsertOIDCSecret(_ context.Context, _ string) error   { return nil }
func (crudFakeRuntime) DecryptOIDCSecret(_ context.Context) (string, error)  { return "", nil }

func setupCRUD(t *testing.T) (*gin.Engine, *crudFakeRepo) {
	t.Helper()
	repo := newCRUDFakeRepo()
	uc := instance.New(repo, crudFakeRuntime{}, nil, runtime.NewBus(nil), slog.Default())
	h := NewInstanceCRUDHandler(uc, slog.Default())
	r := gin.New()
	r.GET("/api/v1/instances/:name", h.Get)
	r.POST("/api/v1/instances", h.Create)
	r.PUT("/api/v1/instances/:name", h.Update)
	r.DELETE("/api/v1/instances/:name", h.Delete)
	return r, repo
}

func doJSON(t *testing.T, r *gin.Engine, method, url string, body any, headers map[string]string) *httptest.ResponseRecorder {
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

func createBody(name string) map[string]any {
	return map[string]any{
		"name": name, "url": "http://sonarr:8989", "api_key": "abc",
		"timeout_sec": 10, "search_timeout_sec": 60,
		"health_check": map[string]any{
			"recheck_auth_sec":    60,
			"recheck_network_sec": 60,
		},
	}
}

func TestCRUD_Create_201_PublishesAndMasks(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "***", body["api_key"])
	assert.NotEmpty(t, w.Header().Get("Last-Modified"))
}

func TestCRUD_Create_DuplicateName_409(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "DUPLICATE_NAME")
}

func TestCRUD_Create_MissingField_400(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances",
		map[string]any{"name": "alpha", "url": "http://x"}, nil) // no api_key
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCRUD_Get_MasksKeyAndSetsLastModified(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	w := doJSON(t, r, http.MethodGet, "/api/v1/instances/alpha", nil, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "***", body["api_key"])
	lm := w.Header().Get("Last-Modified")
	require.NotEmpty(t, lm)
	_, err := http.ParseTime(lm)
	assert.NoError(t, err, "Last-Modified must parse as RFC1123: %q", lm)
}

func TestCRUD_Put_NameImmutable_400(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	w := doJSON(t, r, http.MethodPut, "/api/v1/instances/alpha", createBody("beta"), nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "NAME_IMMUTABLE")
}

func TestCRUD_Put_EmptyKey_Preserves(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	body := createBody("alpha")
	body["api_key"] = ""
	body["url"] = "http://updated:8989"
	w := doJSON(t, r, http.MethodPut, "/api/v1/instances/alpha", body, nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "http://updated:8989", got["url"])
	assert.Equal(t, "***", got["api_key"])
}

func TestCRUD_Put_Stale_IfUnmodifiedSince_412(t *testing.T) {
	t.Parallel()
	r, repo := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	// Force the stored timestamp into the future so any IUS in the past
	// is stale by construction.
	storedAt := time.Now().UTC().Add(time.Hour)
	repo.updated["alpha"] = storedAt
	body := createBody("alpha")
	w := doJSON(t, r, http.MethodPut, "/api/v1/instances/alpha", body,
		map[string]string{"If-Unmodified-Since": time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat)})
	assert.Equal(t, http.StatusPreconditionFailed, w.Code)
	assert.Contains(t, w.Body.String(), "STALE_WRITE")
	// B-5: 412 must carry the current Last-Modified so the SPA can
	// retry with the fresh IUS instead of issuing a separate GET.
	lm := w.Header().Get("Last-Modified")
	require.NotEmpty(t, lm, "412 must include Last-Modified for client retry")
	parsed, err := http.ParseTime(lm)
	require.NoError(t, err, "Last-Modified must parse as RFC1123: %q", lm)
	assert.True(t, parsed.Equal(storedAt.Truncate(time.Second)),
		"Last-Modified must match the current stored row's updated_at")
}

func TestCRUD_Delete_SoleInstance_204(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	w := doJSON(t, r, http.MethodDelete, "/api/v1/instances/alpha", nil, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestCRUD_Delete_NonLast_204(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("beta"), nil)
	w := doJSON(t, r, http.MethodDelete, "/api/v1/instances/alpha", nil, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestCRUD_MalformedBody_400(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/instances", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- Part 028i: uncovered branches ---

// TestCRUD_Get_NotFound exercises the Get handler's writeError path when
// the instance does not exist (ErrNotFound → 404).
func TestCRUD_Get_NotFound(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	w := doJSON(t, r, http.MethodGet, "/api/v1/instances/ghost", nil, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "INSTANCE_NOT_FOUND")
}

// TestCRUD_WriteError_InternalError confirms that a generic error on Delete
// returns 500 via the internal-error branch of writeError.
func TestCRUD_WriteError_InternalError(t *testing.T) {
	t.Parallel()
	// Use a broken repo that returns a generic error on Delete.
	repo := &crudBrokenDelete{crudFakeRepo: *newCRUDFakeRepo()}
	uc := instance.New(repo, crudFakeRuntime{}, nil, runtime.NewBus(nil), slog.Default())
	h := NewInstanceCRUDHandler(uc, slog.Default())
	r := gin.New()
	r.DELETE("/api/v1/instances/:name", h.Delete)

	// Seed one row directly so Delete is reached.
	repo.rows["alpha"] = runtime.InstanceSnapshot{Name: "alpha"}
	repo.count = 1

	w := doJSON(t, r, http.MethodDelete, "/api/v1/instances/alpha", nil, nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

type crudBrokenDelete struct{ crudFakeRepo }

func (f *crudBrokenDelete) Delete(_ context.Context, _ string) error {
	return errors.New("db exploded")
}

// TestCRUD_readJSONBody_WrongContentType exercises the content-type guard.
func TestCRUD_readJSONBody_WrongContentType(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/instances", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "text/xml")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
}

// TestCRUD_Update_IUS_BadFormat exercises the http.ParseTime failure branch
// in Update when the If-Unmodified-Since header cannot be parsed.
func TestCRUD_Update_IUS_BadFormat(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	w := doJSON(t, r, http.MethodPut, "/api/v1/instances/alpha", createBody("alpha"),
		map[string]string{"If-Unmodified-Since": "not-a-date"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "If-Unmodified-Since")
}

// TestCRUD_readJSONBody_TooLarge exercises the MaxBytesError branch in
// readJSONBody by sending a body larger than instanceBodyLimit (64 KiB).
func TestCRUD_readJSONBody_TooLarge(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	body := bytes.Repeat([]byte("x"), instanceBodyLimit+1)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/instances", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
}

// TestCRUD_Create_TypedCode_TimeoutOutOfRange locks H-2 + H-3: a
// timeout_sec of 301 (above the 300s max) returns 400 + the typed
// per-field code, not the generic BAD_REQUEST sentinel.
func TestCRUD_Create_TypedCode_TimeoutOutOfRange(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	body := createBody("alpha")
	body["timeout_sec"] = 301
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", body, nil)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "INVALID_INSTANCE_TIMEOUT_OUT_OF_RANGE", resp["code"],
		"per-field code must reach the wire via errors.As branch")
}

// TestCRUD_Create_TypedCode_RateLimitRPMOutOfRange exercises the
// rate_limit_rpm bound (max 10000). Same contract as above.
func TestCRUD_Create_TypedCode_RateLimitRPMOutOfRange(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	body := createBody("alpha")
	body["rate_limit_rpm"] = 10001
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", body, nil)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "INVALID_INSTANCE_RATE_LIMIT_RPM_OUT_OF_RANGE", resp["code"])
}

// TestCRUD_Create_TypedCode_RetryMaxAttemptsOutOfRange exercises the
// nested retry.max_attempts bound (max 10).
func TestCRUD_Create_TypedCode_RetryMaxAttemptsOutOfRange(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	body := createBody("alpha")
	body["retry"] = map[string]any{"max_attempts": 11}
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", body, nil)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "INVALID_INSTANCE_RETRY_MAX_ATTEMPTS_OUT_OF_RANGE", resp["code"])
}

// TestCRUD_Create_TypedCode_ReservedName locks L-3 end-to-end:
// reserved name "test" surfaces the typed reserved-name code on the
// wire (not the generic BAD_REQUEST it produced before).
func TestCRUD_Create_TypedCode_ReservedName(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	body := createBody("test") // reserved
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", body, nil)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "INVALID_INSTANCE_NAME_RESERVED", resp["code"])
}

// TestCRUD_Create_OmitsHealthCheck_201 locks H-3 at the handler level:
// a POST body that OMITS health_check entirely returns 201.
func TestCRUD_Create_OmitsHealthCheck_201(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	body := map[string]any{
		"name": "alpha", "url": "http://sonarr:8989", "api_key": "abc",
		// no timeout_sec, no search_timeout_sec, no health_check —
		// every zero value must flow through ApplyInstanceDefaults
		// before validation.
	}
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", body, nil)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
}

// TestCRUD_Put_MaskedKey_Preserves is the wire-level regression guard
// for the 2026-05-26 incident: a PUT carrying api_key="***" (the
// masked GET response leaking back through the SPA) must not overwrite
// the stored ciphertext. The handler returns 200 and the masked
// representation; the use-case-level warning is exercised separately.
func TestCRUD_Put_MaskedKey_Preserves(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	body := createBody("alpha")
	body["api_key"] = "***"
	body["mode"] = "manual" // also flip mode to mirror the real incident
	w := doJSON(t, r, http.MethodPut, "/api/v1/instances/alpha", body, nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "***", got["api_key"], "wire response stays masked")
	assert.Equal(t, "manual", got["mode"], "non-secret field still applied")
}

// --- 041a: Phase 11 instance field handler coverage ---

// TestCRUD_Create_NewFields_Defaults verifies a Create payload that
// omits all three new fields is accepted, persisted with the migration
// default (`webhook_install_enabled=true`), and rendered with the
// expected null/derived shape on the response.
func TestCRUD_Create_NewFields_Defaults(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances",
		createBody("alpha"), nil)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Nil(t, body["public_url"],
		"omitted public_url must round-trip as JSON null")
	assert.Equal(t, true, body["webhook_install_enabled"],
		"omitted webhook_install_enabled must default to true")
	assert.Nil(t, body["webhook_url_override"])
	// ui_url falls back to URL when public_url is absent.
	assert.Equal(t, "http://sonarr:8989", body["ui_url"])
}

// TestCRUD_Create_NewFields_PersistAndDerive seeds a Create payload
// with non-default values for all three fields and asserts each one
// round-trips through POST→GET and that ui_url derives from public_url
// when set.
func TestCRUD_Create_NewFields_PersistAndDerive(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)

	body := createBody("alpha")
	body["public_url"] = "https://sonarr.example.com"
	body["webhook_install_enabled"] = false
	body["webhook_url_override"] = "https://seasonfill.example.com"

	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", body, nil)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "https://sonarr.example.com", got["public_url"])
	assert.Equal(t, false, got["webhook_install_enabled"])
	assert.Equal(t, "https://seasonfill.example.com", got["webhook_url_override"])
	assert.Equal(t, "https://sonarr.example.com", got["ui_url"],
		"ui_url must derive from public_url when set")

	// GET should reproduce the same shape.
	w = doJSON(t, r, http.MethodGet, "/api/v1/instances/alpha", nil, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var get map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &get))
	assert.Equal(t, "https://sonarr.example.com", get["public_url"])
	assert.Equal(t, false, get["webhook_install_enabled"])
	assert.Equal(t, "https://seasonfill.example.com", get["webhook_url_override"])
	assert.Equal(t, "https://sonarr.example.com", get["ui_url"])
}

// TestCRUD_Update_WebhookInstallEnabled_PointerFalseHonoured guards the
// pointer-vs-zero-value semantics: an Update body with the field
// explicitly set to false must persist as false, not be silently
// rewritten by the default-applier.
func TestCRUD_Update_WebhookInstallEnabled_PointerFalseHonoured(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)

	body := createBody("alpha")
	body["webhook_install_enabled"] = false
	w := doJSON(t, r, http.MethodPut, "/api/v1/instances/alpha", body, nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	w = doJSON(t, r, http.MethodGet, "/api/v1/instances/alpha", nil, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, false, got["webhook_install_enabled"])
}

// TestCRUD_Create_NewFields_ValidationCases is the table-driven sweep
// of every 400 path on the three new fields. Each case is asserted to
// surface the matching INVALID_INSTANCE_* code so the SPA can render
// per-field feedback without parsing free-form messages.
func TestCRUD_Create_NewFields_ValidationCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  string
		val  string
		code string
	}{
		{"public_url trailing slash", "public_url", "https://sonarr.example.com/", "INVALID_INSTANCE_PUBLIC_URL"},
		{"public_url bad scheme", "public_url", "ftp://sonarr.example.com", "INVALID_INSTANCE_PUBLIC_URL"},
		{"public_url empty string", "public_url", "", "INVALID_INSTANCE_PUBLIC_URL"},
		{"public_url userinfo", "public_url", "https://u:p@sonarr.example.com", "INVALID_INSTANCE_PUBLIC_URL"},
		{"webhook_url_override trailing slash", "webhook_url_override", "https://seasonfill.example.com/", "INVALID_INSTANCE_WEBHOOK_URL_OVERRIDE"},
		{"webhook_url_override bad scheme", "webhook_url_override", "ftp://seasonfill.example.com", "INVALID_INSTANCE_WEBHOOK_URL_OVERRIDE"},
		{"webhook_url_override empty string", "webhook_url_override", "", "INVALID_INSTANCE_WEBHOOK_URL_OVERRIDE"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, _ := setupCRUD(t)
			body := createBody("alpha")
			body[tc.key] = tc.val
			w := doJSON(t, r, http.MethodPost, "/api/v1/instances", body, nil)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.code)
		})
	}
}
