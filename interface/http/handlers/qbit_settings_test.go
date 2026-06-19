package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

const qbitHandlerTestMasterKey = "handler-master-key-32-bytes-aes-gcm"

func newHandlerTestCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(qbitHandlerTestMasterKey)
	require.NoError(t, err)
	return c
}

// --- in-process repos (mirror the use case test fakes, duplicated to
// stay package-local; handler-test isolation outweighs DRY here).

type qbitFakeSettings struct {
	rows map[uint]ports.QbitSettingsRecord
}

func newQbitFakeSettings() *qbitFakeSettings {
	return &qbitFakeSettings{rows: map[uint]ports.QbitSettingsRecord{}}
}

func (f *qbitFakeSettings) Upsert(_ context.Context, rec ports.QbitSettingsRecord) error {
	if rec.ID == 0 {
		rec.ID = uint(len(f.rows) + 1)
	}
	f.rows[rec.InstanceID] = rec
	return nil
}
func (f *qbitFakeSettings) GetByInstance(_ context.Context, id uint) (ports.QbitSettingsRecord, error) {
	r, ok := f.rows[id]
	if !ok {
		// Mirror the F-2b repo: typed error joined with the sentinel.
		return ports.QbitSettingsRecord{}, errors.Join(
			&sharedErrors.QbitSettingsNotFoundError{InstanceID: id},
			ports.ErrNotFound,
		)
	}
	return r, nil
}
func (f *qbitFakeSettings) DeleteByInstance(_ context.Context, id uint) error {
	if _, ok := f.rows[id]; !ok {
		return errors.Join(
			&sharedErrors.QbitSettingsNotFoundError{InstanceID: id},
			ports.ErrNotFound,
		)
	}
	delete(f.rows, id)
	return nil
}
func (f *qbitFakeSettings) List(_ context.Context) ([]ports.QbitSettingsRecord, error) {
	return nil, nil
}

type qbitFakeInstances struct {
	rows map[string]runtime.InstanceSnapshot
}

func newQbitFakeInstances() *qbitFakeInstances {
	return &qbitFakeInstances{rows: map[string]runtime.InstanceSnapshot{}}
}
func (f *qbitFakeInstances) Seed(name string, id uint) {
	f.rows[name] = runtime.InstanceSnapshot{ID: id, Name: name, URL: "http://sonarr"}
}
func (f *qbitFakeInstances) GetByName(_ context.Context, name string, _ *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	r, ok := f.rows[name]
	if !ok {
		return runtime.InstanceSnapshot{}, errors.Join(
			&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
			ports.ErrNotFound,
		)
	}
	return r, nil
}
func (f *qbitFakeInstances) List(_ context.Context, _ *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	return nil, nil
}
func (f *qbitFakeInstances) Create(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher) (uint, error) {
	return 0, nil
}
func (f *qbitFakeInstances) UpdateWithOptions(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher, _ bool, _ *time.Time) error {
	return nil
}
func (f *qbitFakeInstances) Delete(_ context.Context, _ string) error { return nil }
func (f *qbitFakeInstances) GetUpdatedAt(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, nil
}

type webhookStub struct {
	installed atomic.Bool
}

func (s *webhookStub) IsInstalled(_ context.Context, _ domain.InstanceName) (bool, error) {
	return s.installed.Load(), nil
}

// testFixture bundles the whole stack the route exercises.
type testFixture struct {
	t         *testing.T
	settings  *qbitFakeSettings
	instances *qbitFakeInstances
	checker   *webhookStub
	uc        *regrab.SettingsUseCase
	handler   *QbitSettingsHandler
	engine    *gin.Engine
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	settings := newQbitFakeSettings()
	instances := newQbitFakeInstances()
	instances.Seed("alpha", 7)
	checker := &webhookStub{}
	checker.installed.Store(true) // happy-path default
	uc := regrab.NewSettingsUseCase(settings, instances, newHandlerTestCipher(t), nil).
		WithWebhookChecker(checker)
	h := NewQbitSettingsHandler(uc, nil)
	r := gin.New()
	// F-2c-1: mount the typed-error response middleware so the handler's
	// c.Error(err) dispatch reaches the JSON envelope writer.
	r.Use(middleware.ErrorResponseMiddleware(slog.Default()))
	r.GET("/api/v1/instances/:name/qbit/settings", h.Get)
	r.PUT("/api/v1/instances/:name/qbit/settings", h.Upsert)
	r.DELETE("/api/v1/instances/:name/qbit/settings", h.Delete)
	return &testFixture{
		t: t, settings: settings, instances: instances,
		checker: checker, uc: uc, handler: h, engine: r,
	}
}

func (f *testFixture) do(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(f.t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequestWithContext(f.t.Context(), method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func validUpsertBody() dto.QbitSettingsUpsertRequest {
	return dto.QbitSettingsUpsertRequest{
		Enabled:                false,
		URL:                    "http://qbit.local:8080",
		QbitPublicURL:          "https://qbit.example.com",
		Username:               "admin",
		Password:               "hunter2",
		Category:               "sonarr",
		PollIntervalMinutes:    30,
		RegrabCooldownHours:    120,
		MaxConsecutiveNoBetter: 3,
		CustomUnregisteredMsgs: []string{"раздача погашена"},
	}
}

// F-2c-1: the typed-error middleware emits {"error":"<slug>","message":...}
// where <slug> is the lowercase typed code (was the SCREAMING_CASE `code`
// field before the migration). Tests assert the slug on the `error` key.

func TestHandler_GetReturnsNotFoundOnUnknownInstance(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	w := f.do(http.MethodGet, "/api/v1/instances/nope/qbit/settings", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
	var resp dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// F-2c-1: the SettingsUseCase wraps GetByName's typed error with
	// `fmt.Errorf("instance %q: %w", name, ports.ErrNotFound)` which
	// drops the typed chain — so the middleware falls back to the
	// generic `not_found` slug. F-2c-2 will preserve the typed chain
	// at the application layer; F-2c-1 must NOT touch application/**
	// (per story anti-acceptance), so this assertion documents the
	// transitional state.
	assert.Equal(t, "not_found", resp.Error)
}

func TestHandler_GetReturnsNotFoundWhenSettingsAbsent(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	w := f.do(http.MethodGet, "/api/v1/instances/alpha/qbit/settings", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
	var resp dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "qbit_settings_not_found", resp.Error)
}

func TestHandler_PutCreatesAnonRow(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	body := validUpsertBody()
	body.Password = ""
	body.Username = ""
	w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", body)
	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.QbitSettingsDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.PasswordSet)
	assert.Empty(t, resp.Username)
}

func TestHandler_PutCreatesRowWithCredentials(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", validUpsertBody())
	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.QbitSettingsDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.PasswordSet)
	assert.Equal(t, "admin", resp.Username)
	assert.NotContains(t, w.Body.String(), "hunter2",
		"plaintext password must never appear in response")
}

func TestHandler_PutUpdateKeepsPasswordOnEmpty(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	// seed
	w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", validUpsertBody())
	require.Equal(t, http.StatusOK, w.Code)
	originalBlob := append([]byte{}, f.settings.rows[7].PasswordEncrypted...)

	body := validUpsertBody()
	body.Password = ""
	body.URL = "http://qbit2.local:8080"
	w = f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", body)
	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.QbitSettingsDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.PasswordSet)
	assert.Equal(t, "http://qbit2.local:8080", resp.URL)
	assert.Equal(t, originalBlob, f.settings.rows[7].PasswordEncrypted)
}

func TestHandler_PutUpdateChangesPasswordOnNonEmpty(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", validUpsertBody())
	require.Equal(t, http.StatusOK, w.Code)
	originalBlob := append([]byte{}, f.settings.rows[7].PasswordEncrypted...)

	body := validUpsertBody()
	body.Password = "newpass"
	w = f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", body)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEqual(t, originalBlob, f.settings.rows[7].PasswordEncrypted)
}

func TestHandler_PutEnableWithoutWebhookReturns409(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	f.checker.installed.Store(false)
	body := validUpsertBody()
	body.Enabled = true
	w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", body)
	require.Equal(t, http.StatusConflict, w.Code)
	var resp dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "WEBHOOK_NOT_INSTALLED", resp.Code)
	assert.Empty(t, f.settings.rows, "must not persist when gate fails")
}

func TestHandler_PutEnableWithWebhookSucceeds(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	f.checker.installed.Store(true)
	body := validUpsertBody()
	body.Enabled = true
	w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", body)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandler_PutValidationErrors_UseCaseCodes(t *testing.T) {
	t.Parallel()
	// These cases pass the F-3 validator middleware (tag-level checks)
	// and reach the use case, which enforces domain-specific bounds and
	// emits the typed wire code.
	cases := []struct {
		name string
		mut  func(*dto.QbitSettingsUpsertRequest)
		code string
	}{
		// scheme rejection — validator's `url` tag accepts ftp; use case
		// rejects on http/https-only.
		{"bad scheme", func(b *dto.QbitSettingsUpsertRequest) { b.URL = "ftp://x" }, "INVALID_QBIT_URL"},
		// 1 minute passes `gt=0,lte=1440` but fails the use case minimum
		// of 5 minutes → use case rejects with INVALID_POLL_INTERVAL.
		{"poll too small", func(b *dto.QbitSettingsUpsertRequest) { b.PollIntervalMinutes = 1 }, "INVALID_POLL_INTERVAL"},
		// 725 hours passes validator's `lte=8760` cap but fails the use
		// case 30-day cap → use case rejects with INVALID_REGRAB_COOLDOWN.
		{"cooldown above use-case cap", func(b *dto.QbitSettingsUpsertRequest) { b.RegrabCooldownHours = 725 }, "INVALID_REGRAB_COOLDOWN"},
		// public_url scheme rejection — validator `omitempty,url` accepts
		// ftp; use case requires http/https.
		{"public_url bad scheme", func(b *dto.QbitSettingsUpsertRequest) { b.QbitPublicURL = "ftp://x" }, "INVALID_QBIT_PUBLIC_URL"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newTestFixture(t)
			body := validUpsertBody()
			tc.mut(&body)
			w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", body)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			var resp dto.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, tc.code, resp.Code)
		})
	}
}

// TestHandler_PutValidationErrors_ValidatorTagged covers the F-3
// validator middleware path: tag-level rejections produce the structured
// {error:"validation_failed", fields[]} envelope instead of the legacy
// per-field code envelope.
func TestHandler_PutValidationErrors_ValidatorTagged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		mut   func(*dto.QbitSettingsUpsertRequest)
		field string
		tag   string
	}{
		{"empty url", func(b *dto.QbitSettingsUpsertRequest) { b.URL = "" }, "url", "required"},
		{"empty category", func(b *dto.QbitSettingsUpsertRequest) { b.Category = "" }, "category", "required"},
		{"cooldown above validator cap", func(b *dto.QbitSettingsUpsertRequest) { b.RegrabCooldownHours = 9999 }, "regrab_cooldown_hours", "lte"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newTestFixture(t)
			body := validUpsertBody()
			tc.mut(&body)
			w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", body)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			var got struct {
				Error  string `json:"error"`
				Fields []struct {
					Field string `json:"field"`
					Tag   string `json:"tag"`
				} `json:"fields"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			assert.Equal(t, "validation_failed", got.Error)
			require.NotEmpty(t, got.Fields)
			seen := map[string]string{}
			for _, fe := range got.Fields {
				seen[fe.Field] = fe.Tag
			}
			assert.Equal(t, tc.tag, seen[tc.field],
				"field %q must surface tag %q; got %+v", tc.field, tc.tag, got.Fields)
		})
	}
}

func TestHandler_Delete(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", validUpsertBody())
	require.Equal(t, http.StatusOK, w.Code)

	w = f.do(http.MethodDelete, "/api/v1/instances/alpha/qbit/settings", nil)
	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, f.settings.rows)
}

func TestHandler_DeleteNotFound(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	w := f.do(http.MethodDelete, "/api/v1/instances/alpha/qbit/settings", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
	var resp dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// F-2c-1: typed-error middleware emits the slug on the `error`
	// key, not the legacy SCREAMING_CASE Code field.
	assert.Equal(t, "qbit_settings_not_found", resp.Error)
}

func TestHandler_GetReturnsCreatedRow(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", validUpsertBody())
	require.Equal(t, http.StatusOK, w.Code)
	w = f.do(http.MethodGet, "/api/v1/instances/alpha/qbit/settings", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.QbitSettingsDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.PasswordSet)
	assert.Equal(t, "http://qbit.local:8080", resp.URL)
	assert.Equal(t, "https://qbit.example.com", resp.QbitPublicURL)
	assert.Equal(t, []string{"раздача погашена"}, resp.CustomUnregisteredMsgs)
}

// TestHandler_PutAcceptsEmptyPublicURL guards the F-P2-1 opt-in path:
// instances that haven't enrolled the new field must round-trip a PUT
// with an empty qbit_public_url and read back the empty value.
func TestHandler_PutAcceptsEmptyPublicURL(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	body := validUpsertBody()
	body.QbitPublicURL = ""
	w := f.do(http.MethodPut, "/api/v1/instances/alpha/qbit/settings", body)
	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.QbitSettingsDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "", resp.QbitPublicURL)
}

func TestHandler_PutRejectsWrongContentType(t *testing.T) {
	t.Parallel()
	f := newTestFixture(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/api/v1/instances/alpha/qbit/settings",
		bytes.NewReader([]byte(`{"url":"http://x"}`)))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
