package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	domainwebhook "github.com/alexmorbo/seasonfill/domain/webhook"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/config"
)

type fakeProcessor struct {
	mu       sync.Mutex
	calls    int
	lastEvt  domainwebhook.Event
	returnFn func(evt domainwebhook.Event) error
}

func (f *fakeProcessor) Process(_ context.Context, evt domainwebhook.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastEvt = evt
	if f.returnFn != nil {
		return f.returnFn(evt)
	}
	return nil
}

type webhookFixture struct {
	proc   *fakeProcessor
	router *gin.Engine
}

func newWebhookFixture(t *testing.T, withAuth bool, allowed []string) *webhookFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := config.WebhookConfig{AllowedInstances: allowed}
	if withAuth {
		cfg.Secret = "hook-secret"
	}

	proc := &fakeProcessor{}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewWebhookHandler(proc, cfg, lg)

	r := gin.New()
	api := r.Group("/api/v1")
	wh := api.Group("/webhook/sonarr/:instance_name")
	if cfg.Secret != "" {
		wh.Use(middleware.APIKeyAuth(cfg.Secret))
	}
	wh.POST("", h.Handle)

	return &webhookFixture{proc: proc, router: r}
}

func (f *webhookFixture) post(t *testing.T, instance, key string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/sonarr/"+instance, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func importedPayload() []byte {
	return []byte(`{"eventType":"Download","instanceName":"ignored","downloadId":"ABC123","series":{"id":122,"title":"Hijack"},"episodes":[{"id":1,"seasonNumber":2,"episodeNumber":4}],"episodeFile":{"id":9876,"quality":"WEBDL-2160p"}}`)
}

func importFailedPayload() []byte {
	return []byte(`{"eventType":"ManualInteractionRequired","instanceName":"ignored","downloadId":"ABC123","series":{"id":122},"episodes":[{"id":1,"seasonNumber":2}],"downloadStatusMessages":[{"title":"Audio","messages":["bad"]}]}`)
}

func unsupportedPayload() []byte {
	return []byte(`{"eventType":"Rename","instanceName":"ignored","series":{"id":122}}`)
}

// --- Happy paths ----------------------------------------------------------

func TestWebhookHandler_Imported_200(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	w := f.post(t, "sonarr-main", "", importedPayload())
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"ok": true}`, w.Body.String())
	assert.Equal(t, 1, f.proc.calls)
	assert.Equal(t, domainwebhook.EventTypeImported, f.proc.lastEvt.Type)
	assert.Equal(t, "sonarr-main", f.proc.lastEvt.InstanceName,
		"InstanceName must come from URL path, not payload")
	assert.Equal(t, "ABC123", f.proc.lastEvt.DownloadID)
}

func TestWebhookHandler_ImportFailed_200(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	w := f.post(t, "sonarr-main", "", importFailedPayload())
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, domainwebhook.EventTypeImportFailed, f.proc.lastEvt.Type)
}

func TestWebhookHandler_UnsupportedEvent_200(t *testing.T) {
	// Mapper returns (Event{Type: Unsupported}, nil) on Rename per
	// 007a's "no ErrUnsupportedEventType" decision; UC no-ops.
	f := newWebhookFixture(t, false, nil)
	w := f.post(t, "sonarr-main", "", unsupportedPayload())
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, f.proc.calls)
	assert.Equal(t, domainwebhook.EventTypeUnsupported, f.proc.lastEvt.Type)
}

// --- 400 paths ------------------------------------------------------------

func TestWebhookHandler_MalformedJSON_400(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	w := f.post(t, "sonarr-main", "", []byte(`{"eventType":`))
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "malformed payload", body["error"])
	assert.Zero(t, f.proc.calls)
}

func TestWebhookHandler_EmptyBody_400(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	w := f.post(t, "sonarr-main", "", nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Zero(t, f.proc.calls)
}

func TestWebhookHandler_MissingEventType_400(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	w := f.post(t, "sonarr-main", "", []byte(`{"instanceName":"x"}`))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Zero(t, f.proc.calls)
}

func TestWebhookHandler_DisallowedInstance_404(t *testing.T) {
	f := newWebhookFixture(t, false, []string{"sonarr-main", "sonarr-tv"})
	w := f.post(t, "sonarr-rogue", "", importedPayload())
	require.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "unknown instance", body["error"])
	assert.Zero(t, f.proc.calls)
}

func TestWebhookHandler_OversizeBody_400(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	// 2 MiB exceeds the 1 MiB cap.
	oversized := bytes.Repeat([]byte("x"), 2<<20)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/sonarr/sonarr-main", bytes.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "payload too large", body["error"])
	assert.Zero(t, f.proc.calls)
}

func TestWebhookHandler_AllowedInstance_200(t *testing.T) {
	f := newWebhookFixture(t, false, []string{"sonarr-main", "sonarr-tv"})
	w := f.post(t, "sonarr-tv", "", importedPayload())
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, f.proc.calls)
}

func TestWebhookHandler_EmptyAllowList_AcceptsAny(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	w := f.post(t, "sonarr-anything", "", importedPayload())
	require.Equal(t, http.StatusOK, w.Code)
}

// --- Auth paths -----------------------------------------------------------

func TestWebhookHandler_Auth_MissingKey_401(t *testing.T) {
	f := newWebhookFixture(t, true, nil)
	w := f.post(t, "sonarr-main", "", importedPayload())
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Zero(t, f.proc.calls)
}

func TestWebhookHandler_Auth_WrongKey_401(t *testing.T) {
	f := newWebhookFixture(t, true, nil)
	w := f.post(t, "sonarr-main", "wrong-key", importedPayload())
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Zero(t, f.proc.calls)
}

func TestWebhookHandler_Auth_CorrectKey_200(t *testing.T) {
	f := newWebhookFixture(t, true, nil)
	w := f.post(t, "sonarr-main", "hook-secret", importedPayload())
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, f.proc.calls)
}

func TestWebhookHandler_NoAuth_AcceptsRequest(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	w := f.post(t, "sonarr-main", "", importedPayload())
	require.Equal(t, http.StatusOK, w.Code)
}

// --- 5xx + metric paths --------------------------------------------------

func TestWebhookHandler_TransientUseCaseError_500(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	f.proc.returnFn = func(_ domainwebhook.Event) error {
		return fmt.Errorf("match: %w: %w", ports.ErrDBUnavailable, errors.New("conn refused"))
	}
	w := f.post(t, "sonarr-main", "", importedPayload())
	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "transient failure, retry", body["error"])
}

func TestWebhookHandler_NonTransientUseCaseError_200_EmitsMetric(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	f.proc.returnFn = func(_ domainwebhook.Event) error {
		return errors.New("some logic error")
	}
	w := f.post(t, "sonarr-main", "", importedPayload())
	require.Equal(t, http.StatusOK, w.Code,
		"non-transient must NOT 500 — Sonarr retries would pollute the failure rate")

	mrouter := gin.New()
	mrouter.GET("/metrics", MetricsHandler())
	mreq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	mw := httptest.NewRecorder()
	mrouter.ServeHTTP(mw, mreq)
	body := mw.Body.String()
	assert.Contains(t, body, "seasonfill_webhook_processing_failures_total")
	assert.Contains(t, body, `instance="sonarr-main"`)
	assert.Contains(t, body, `error_kind="other"`)
}

// --- Race smoke -----------------------------------------------------------

func TestWebhookHandler_Concurrent_Race(t *testing.T) {
	f := newWebhookFixture(t, false, nil)
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			body := []byte(strings.Replace(string(importedPayload()),
				`"ABC123"`, fmt.Sprintf(`"ABC%03d"`, i), 1))
			w := f.post(t, "sonarr-main", "", body)
			require.Equal(t, http.StatusOK, w.Code)
		}(i)
	}
	wg.Wait()
	f.proc.mu.Lock()
	calls := f.proc.calls
	f.proc.mu.Unlock()
	assert.Equal(t, n, calls)
}
