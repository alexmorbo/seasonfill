package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

type aggFakeNotifier struct {
	notifications []sonarr.Notification
}

func (n *aggFakeNotifier) ListNotifications(context.Context) ([]sonarr.Notification, error) {
	return n.notifications, nil
}
func (n *aggFakeNotifier) CreateNotification(_ context.Context, w sonarr.NotificationPayload) (sonarr.Notification, error) {
	notif := sonarr.Notification{ID: 1, Implementation: "Webhook"}
	n.notifications = append(n.notifications, notif)
	return notif, nil
}
func (n *aggFakeNotifier) UpdateNotification(_ context.Context, existing sonarr.Notification, w sonarr.NotificationPayload) (sonarr.Notification, error) {
	return existing, nil
}
func (n *aggFakeNotifier) DeleteNotification(context.Context, int) error { return nil }

func TestWebhooksAggregateHandler_MixedStatesRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)

	healthy := &aggFakeNotifier{notifications: []sonarr.Notification{
		{ID: 7, Implementation: "Webhook"},
	}}
	healthyCache := webhookinstall.NewStatusCache()
	id := 7
	url := "https://sf.example/api/v1/webhook/sonarr/homelab"
	healthyCache.Set("homelab", webhookinstall.Status{
		Installed:      true,
		NotificationID: &id,
		InstalledURL:   &url,
		LastCheckedAt:  time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC),
	})

	lookup := func(name string) (runtime.InstanceSnapshot, webhookinstall.SonarrNotifier, bool) {
		if name == "homelab" {
			return runtime.InstanceSnapshot{Name: name, WebhookInstallEnabled: true}, healthy, true
		}
		return runtime.InstanceSnapshot{}, nil, false
	}
	r := webhookinstall.New(webhookinstall.Deps{
		Lookup:    lookup,
		PublicURL: func(context.Context) string { return "https://sf.example" },
		Cache:     healthyCache,
	}).WithClock(func() time.Time { return time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC) })

	h := NewWebhooksAggregateHandler(r, stubLister{"homelab"}, nil)
	engine := gin.New()
	engine.GET("/api/v1/webhooks/status", h.Status)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/webhooks/status", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200, body=%s", w.Code, w.Body.String())
	}
	var got dto.WebhookStatusAggregate
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("len: %d", len(got.Items))
	}
	if !got.Items[0].Installed || !got.Items[0].Healthy {
		t.Errorf("homelab item: %+v", got.Items[0])
	}
	if got.HealthyCount != 1 || got.UnhealthyCount != 0 {
		t.Errorf("counts: %d/%d", got.HealthyCount, got.UnhealthyCount)
	}
}

func TestWebhooksAggregateHandler_UnknownInstanceFallsToUnhealthy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	lookup := func(name string) (runtime.InstanceSnapshot, webhookinstall.SonarrNotifier, bool) {
		return runtime.InstanceSnapshot{}, nil, false
	}
	r := webhookinstall.New(webhookinstall.Deps{
		Lookup:    lookup,
		PublicURL: func(context.Context) string { return "https://sf.example" },
		Cache:     webhookinstall.NewStatusCache(),
	}).WithClock(func() time.Time { return time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC) })

	h := NewWebhooksAggregateHandler(r, stubLister{"missing-instance"}, nil)
	engine := gin.New()
	engine.GET("/api/v1/webhooks/status", h.Status)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/webhooks/status", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200, body=%s", w.Code, w.Body.String())
	}
	var got dto.WebhookStatusAggregate
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Items) != 1 {
		t.Fatalf("len: %d", len(got.Items))
	}
	if got.Items[0].Installed || got.Items[0].Healthy {
		t.Errorf("missing instance: want installed=false healthy=false, got %+v", got.Items[0])
	}
	if got.HealthyCount != 0 || got.UnhealthyCount != 1 {
		t.Errorf("counts: %d/%d", got.HealthyCount, got.UnhealthyCount)
	}
}

func TestWebhooksAggregateHandler_EmptyInstanceList(t *testing.T) {
	gin.SetMode(gin.TestMode)

	lookup := func(name string) (runtime.InstanceSnapshot, webhookinstall.SonarrNotifier, bool) {
		return runtime.InstanceSnapshot{}, nil, false
	}
	r := webhookinstall.New(webhookinstall.Deps{
		Lookup:    lookup,
		PublicURL: func(context.Context) string { return "" },
		Cache:     webhookinstall.NewStatusCache(),
	}).WithClock(func() time.Time { return time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC) })

	h := NewWebhooksAggregateHandler(r, stubLister{}, nil)
	engine := gin.New()
	engine.GET("/api/v1/webhooks/status", h.Status)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/webhooks/status", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var got dto.WebhookStatusAggregate
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Items) != 0 {
		t.Errorf("len: want 0, got %d", len(got.Items))
	}
}
