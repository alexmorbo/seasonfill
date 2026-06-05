package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// newInstallTestRig wires a gin engine with the Install handler and
// a httptest Sonarr server. Returns the engine, the sonarr stub URL
// holder, and a setter so individual tests can rebind the stub.
func newInstallTestRig(t *testing.T, sonarrHandler http.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(sonarrHandler)
	t.Cleanup(srv.Close)
	client := sonarr.New("alpha", srv.URL, "k", 2*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	h := NewWebhookInstallHandler(reg, "api-key-XYZ",
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.POST("/api/v1/instances/:name/webhook/install", h.Install)
	return r
}

func TestWebhookInstall_200NoopOnExisting(t *testing.T) {
	r := newInstallTestRig(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v3/notification" && req.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[
				{"id": 7, "implementation": "Webhook",
				 "fields": [{"name":"url","value":"https://seasonfill.example/api/v1/webhook/sonarr/alpha"}]}
			]`))
			return
		}
		t.Errorf("unexpected sonarr request: %s %s", req.Method, req.URL.Path)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/instances/alpha/webhook/install", nil)
	req.Host = "seasonfill.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, true, got["installed"])
	assert.Equal(t, false, got["created"])
	assert.Equal(t, float64(7), got["notification_id"])
}

func TestWebhookInstall_201CreateNew(t *testing.T) {
	var createBody string
	r := newInstallTestRig(t, func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/api/v3/notification" && req.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case req.URL.Path == "/api/v3/notification" && req.Method == http.MethodPost:
			buf, _ := io.ReadAll(req.Body)
			createBody = string(buf)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":99,"implementation":"Webhook"}`))
		default:
			t.Errorf("unexpected sonarr request: %s %s", req.Method, req.URL.Path)
		}
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/instances/alpha/webhook/install", nil)
	req.Host = "seasonfill.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, true, got["installed"])
	assert.Equal(t, true, got["created"])
	assert.Equal(t, float64(99), got["notification_id"])
	assert.Contains(t, createBody, `"implementation":"Webhook"`)
	assert.Contains(t, createBody, `"onGrab":true`)
	assert.Contains(t, createBody, `https://seasonfill.example/api/v1/webhook/sonarr/alpha`)
	assert.Contains(t, createBody, `X-Api-Key=api-key-XYZ`)
}

func TestWebhookInstall_412PublicURLUndetermined(t *testing.T) {
	r := newInstallTestRig(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("sonarr should not be called when public URL is undetermined")
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/instances/alpha/webhook/install", nil)
	req.Host = "" // explicitly empty — triggers 412
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusPreconditionFailed, w.Code)
	assert.Contains(t, w.Body.String(), "PUBLIC_URL_UNDETERMINED")
}

func TestWebhookInstall_404UnknownInstance(t *testing.T) {
	r := newInstallTestRig(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("sonarr should not be called when instance is unknown")
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/instances/ghost/webhook/install", nil)
	req.Host = "seasonfill.example"
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "unknown instance: ghost")
}

func TestWebhookInstall_502SonarrUnauthorized(t *testing.T) {
	r := newInstallTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/instances/alpha/webhook/install", nil)
	req.Host = "seasonfill.example"
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unauthorized")
}

func TestWebhookInstall_502SonarrNetworkError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := sonarr.New("alpha", "http://127.0.0.1:1", "k", 200*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	h := NewWebhookInstallHandler(reg, "api-key-XYZ",
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.POST("/api/v1/instances/:name/webhook/install", h.Install)

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/instances/alpha/webhook/install", nil)
	req.Host = "seasonfill.example"
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unavailable")
}
