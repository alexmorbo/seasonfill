package handlers

import (
	"context"
	"errors"
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
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
)

// stubNotifier satisfies webhookinstall.SonarrNotifier with the
// minimal surface this file needs. Update + Delete short-circuit to
// zero values — only Install tests exercise them, and not in this
// file.
type stubNotifier struct {
	list       []sonarr.Notification
	createResp sonarr.Notification
	createErr  error
}

func (s *stubNotifier) ListNotifications(context.Context) ([]sonarr.Notification, error) {
	return s.list, nil
}
func (s *stubNotifier) CreateNotification(_ context.Context, _ sonarr.NotificationPayload) (sonarr.Notification, error) {
	return s.createResp, s.createErr
}
func (s *stubNotifier) UpdateNotification(_ context.Context, _ sonarr.Notification, _ sonarr.NotificationPayload) (sonarr.Notification, error) {
	return sonarr.Notification{}, nil
}
func (s *stubNotifier) DeleteNotification(_ context.Context, _ int) error { return nil }

func newInstallTestRig(t *testing.T, snap runtime.InstanceSnapshot, n webhookinstall.SonarrNotifier, publicURL string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cache := webhookinstall.NewStatusCache()
	rec := webhookinstall.New(webhookinstall.Deps{
		Lookup: func(name string) (runtime.InstanceSnapshot, webhookinstall.SonarrNotifier, bool) {
			return snap, n, true
		},
		PublicURL: func(context.Context) string { return publicURL },
		Cache:     cache, APIKey: "key",
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	h := NewWebhookInstallHandler(rec, cache, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.POST("/api/v1/instances/:name/webhook/install", h.Install)
	return r
}

func postInstall(t *testing.T, r *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/instances/alpha/webhook/install", nil)
	r.ServeHTTP(w, req)
	return w
}

func TestWebhookInstall_201CreateNew(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	r := newInstallTestRig(t, snap, &stubNotifier{createResp: sonarr.Notification{ID: 99}}, "https://sf.example")
	w := postInstall(t, r)
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"created":true`)
	assert.Contains(t, w.Body.String(), `"notification_id":99`)
}

func TestWebhookInstall_200NoopOnExisting(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &stubNotifier{
		list: []sonarr.Notification{{
			ID: 42, Implementation: "Webhook",
			Fields: []sonarr.NotificationField{
				{Name: "url", Value: "https://sf.example/api/v1/webhook/sonarr/alpha"},
			},
		}},
	}
	// Create rig manually so we can pre-populate cache
	gin.SetMode(gin.TestMode)
	cache := webhookinstall.NewStatusCache()
	rec := webhookinstall.New(webhookinstall.Deps{
		Lookup: func(name string) (runtime.InstanceSnapshot, webhookinstall.SonarrNotifier, bool) {
			return snap, n, true
		},
		PublicURL: func(context.Context) string { return "https://sf.example" },
		Cache:     cache, APIKey: "key",
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	// Pre-populate cache so prior.NotificationID != nil
	id := 42
	url := "https://sf.example/api/v1/webhook/sonarr/alpha"
	cache.Set("alpha", webhookinstall.Status{
		Installed: true, NotificationID: &id, InstalledURL: &url,
		LastCheckedAt: time.Now(),
	})
	h := NewWebhookInstallHandler(rec, cache, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.POST("/api/v1/instances/:name/webhook/install", h.Install)

	w := postInstall(t, r)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"created":false`)
}

func TestWebhookInstall_502OnSonarrError(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	r := newInstallTestRig(t, snap, &stubNotifier{createErr: errors.New("boom")}, "https://sf.example")
	require.Equal(t, http.StatusBadGateway, postInstall(t, r).Code)
}

func TestWebhookInstall_412OnUnresolvedPublicURL(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	r := newInstallTestRig(t, snap, &stubNotifier{}, "")
	require.Equal(t, http.StatusPreconditionFailed, postInstall(t, r).Code)
}
