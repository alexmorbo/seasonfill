package webhookinstall

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

type fakeReconcileNotifier struct {
	installed bool
	id        int
	listErr   error
}

func (n *fakeReconcileNotifier) ListNotifications(context.Context) ([]sonarr.Notification, error) {
	if n.listErr != nil {
		return nil, n.listErr
	}
	if n.installed {
		return []sonarr.Notification{{ID: n.id, Implementation: "Webhook"}}, nil
	}
	return nil, nil
}

func (n *fakeReconcileNotifier) CreateNotification(_ context.Context, want sonarr.NotificationPayload) (sonarr.Notification, error) {
	n.installed = true
	return sonarr.Notification{ID: n.id, Implementation: "Webhook"}, nil
}

func (n *fakeReconcileNotifier) UpdateNotification(_ context.Context, existing sonarr.Notification, want sonarr.NotificationPayload) (sonarr.Notification, error) {
	return existing, nil
}

func (n *fakeReconcileNotifier) DeleteNotification(context.Context, int) error { return nil }

func TestAggregate_MixedInstalledAndError(t *testing.T) {
	good := &fakeReconcileNotifier{installed: true, id: 7}
	bad := &fakeReconcileNotifier{listErr: errors.New("sonarr 503")}
	lookup := func(name string) (runtime.InstanceSnapshot, SonarrNotifier, bool) {
		switch name {
		case "homelab":
			return runtime.InstanceSnapshot{Name: name, WebhookInstallEnabled: true}, good, true
		case "4k":
			return runtime.InstanceSnapshot{Name: name, WebhookInstallEnabled: true}, bad, true
		}
		return runtime.InstanceSnapshot{}, nil, false
	}
	cache := NewStatusCache()
	r := New(Deps{
		Lookup:    lookup,
		PublicURL: func(context.Context) string { return "https://sf.example" },
		Cache:     cache,
	}).WithClock(func() time.Time { return time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC) })

	items, err := Aggregate(context.Background(), r, []string{"homelab", "4k"})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len: got %d want 2", len(items))
	}
	// Order preservation.
	if items[0].InstanceName != "homelab" || items[1].InstanceName != "4k" {
		t.Errorf("order: %+v", items)
	}
	if !items[0].Healthy {
		t.Errorf("homelab.Healthy: got false; got=%+v", items[0])
	}
	if items[1].Healthy {
		t.Errorf("4k.Healthy: got true; want false")
	}
	if items[1].Error == nil {
		t.Errorf("4k.Error: nil; want populated")
	}
}

func TestAggregate_EmptyNames(t *testing.T) {
	cache := NewStatusCache()
	r := New(Deps{
		Lookup: func(string) (runtime.InstanceSnapshot, SonarrNotifier, bool) {
			return runtime.InstanceSnapshot{}, nil, false
		},
		PublicURL: func(context.Context) string { return "" },
		Cache:     cache,
	})
	items, err := Aggregate(context.Background(), r, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len: got %d want 0", len(items))
	}
}

func TestAggregate_ContextCanceledShortCircuits(t *testing.T) {
	cache := NewStatusCache()
	r := New(Deps{
		Lookup: func(string) (runtime.InstanceSnapshot, SonarrNotifier, bool) {
			return runtime.InstanceSnapshot{Name: "x", WebhookInstallEnabled: true}, &fakeReconcileNotifier{listErr: errors.New("boom")}, true
		},
		PublicURL: func(context.Context) string { return "https://x" },
		Cache:     cache,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Aggregate(ctx, r, []string{"x"}); err == nil {
		t.Fatal("expected ctx.Err() back, got nil")
	}
}
