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

// newStatusTestRig mirrors newInstallTestRig from webhook_install_test.go.
func newStatusTestRig(t *testing.T, sonarrHandler http.HandlerFunc) *gin.Engine {
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
	h := NewWebhookStatusHandler(reg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/webhook/status", h.Status)
	return r
}

func TestWebhookStatus_200InstalledExactMatch(t *testing.T) {
	r := newStatusTestRig(t, func(w http.ResponseWriter, req *http.Request) {
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
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/webhook/status", nil)
	req.Host = "seasonfill.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, true, got["installed"])
	assert.Equal(t, float64(7), got["notification_id"])
	assert.Equal(t, "https://seasonfill.example/api/v1/webhook/sonarr/alpha", got["url"])
}

func TestWebhookStatus_200InstalledPathMatch(t *testing.T) {
	// Stale public URL (old domain) — still matched by canonical path segment.
	r := newStatusTestRig(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v3/notification" {
			_, _ = w.Write([]byte(`[
				{"id": 42, "implementation": "Webhook",
				 "fields": [{"name":"url","value":"https://old.domain.example/api/v1/webhook/sonarr/alpha"}]}
			]`))
			return
		}
		t.Errorf("unexpected sonarr request: %s %s", req.Method, req.URL.Path)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/webhook/status", nil)
	req.Host = "new.domain.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, true, got["installed"])
	assert.Equal(t, float64(42), got["notification_id"])
}

func TestWebhookStatus_200NotInstalled(t *testing.T) {
	r := newStatusTestRig(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v3/notification" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		t.Errorf("unexpected sonarr request: %s %s", req.Method, req.URL.Path)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/webhook/status", nil)
	req.Host = "seasonfill.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, false, got["installed"])
	assert.Nil(t, got["notification_id"])
	assert.Nil(t, got["url"])
}

func TestWebhookStatus_200SkipsNonWebhookNotifications(t *testing.T) {
	r := newStatusTestRig(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v3/notification" {
			_, _ = w.Write([]byte(`[
				{"id": 1, "implementation": "Email",
				 "fields": [{"name":"url","value":"https://seasonfill.example/api/v1/webhook/sonarr/alpha"}]},
				{"id": 2, "implementation": "Slack",
				 "fields": [{"name":"url","value":"https://seasonfill.example/api/v1/webhook/sonarr/alpha"}]}
			]`))
			return
		}
		t.Errorf("unexpected sonarr request: %s %s", req.Method, req.URL.Path)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/webhook/status", nil)
	req.Host = "seasonfill.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, false, got["installed"])
}

func TestWebhookStatus_404UnknownInstance(t *testing.T) {
	r := newStatusTestRig(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("sonarr should not be called when instance is unknown")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/ghost/webhook/status", nil)
	req.Host = "seasonfill.example"
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "unknown instance: ghost")
}

func TestWebhookStatus_502SonarrUnauthorized(t *testing.T) {
	r := newStatusTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/webhook/status", nil)
	req.Host = "seasonfill.example"
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unauthorized")
}

func TestWebhookStatus_502SonarrNetworkError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := sonarr.New("alpha", "http://127.0.0.1:1", "k", 200*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	h := NewWebhookStatusHandler(reg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/webhook/status", h.Status)

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/webhook/status", nil)
	req.Host = "seasonfill.example"
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unavailable")
}
