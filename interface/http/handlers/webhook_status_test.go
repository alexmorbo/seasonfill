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

	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func newStatusRig(t *testing.T, cache *webhookinstall.StatusCache) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := webhookinstall.New(webhookinstall.Deps{
		Lookup: func(name string) (runtime.InstanceSnapshot, webhookinstall.SonarrNotifier, bool) {
			return runtime.InstanceSnapshot{Name: name, WebhookInstallEnabled: true}, &stubNotifier{}, true
		},
		Cache: cache, APIKey: "k",
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	h := NewWebhookStatusHandler(rec, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/webhook/status", h.Status)
	return r
}

func getStatus(t *testing.T, r *gin.Engine) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/webhook/status", nil)
	r.ServeHTTP(w, req)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return w.Code, got
}

func TestWebhookStatus_ReadsCache(t *testing.T) {
	t.Parallel()
	cache := webhookinstall.NewStatusCache()
	id := 7
	url := "https://sf.example/api/v1/webhook/sonarr/alpha"
	cache.Set("alpha", webhookinstall.Status{
		Installed: true, NotificationID: &id, InstalledURL: &url, LastCheckedAt: time.Now(),
	})
	code, got := getStatus(t, newStatusRig(t, cache))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, got["installed"])
	assert.Equal(t, float64(7), got["notification_id"])
}

func TestWebhookStatus_PropagatesLastError(t *testing.T) {
	t.Parallel()
	cache := webhookinstall.NewStatusCache()
	msg := "sonarr unauthorized"
	future := time.Now().Add(time.Hour)
	cache.Set("alpha", webhookinstall.Status{
		LastError: &msg, LastCheckedAt: time.Now(), NextRetryAt: &future,
	})
	code, got := getStatus(t, newStatusRig(t, cache))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, false, got["installed"])
	assert.Equal(t, "sonarr unauthorized", got["error"])
}
