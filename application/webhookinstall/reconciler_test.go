package webhookinstall

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// fakeNotifier records the most recent Create/Update/Delete call and
// replays the configured List/Create/Update/Delete responses. Pointer
// fields on *Call expose what the reconciler sent; *Err / *Resp drive
// the return values.
type fakeNotifier struct {
	list           []sonarr.Notification
	listErr        error
	createCall     *sonarr.NotificationPayload
	createResp     sonarr.Notification
	createErr      error
	updateExisting *sonarr.Notification
	updateResp     sonarr.Notification
	updateErr      error
	deleteIDs      []int
	deleteErr      error
}

func (f *fakeNotifier) ListNotifications(context.Context) ([]sonarr.Notification, error) {
	return f.list, f.listErr
}
func (f *fakeNotifier) CreateNotification(_ context.Context, p sonarr.NotificationPayload) (sonarr.Notification, error) {
	pp := p
	f.createCall = &pp
	return f.createResp, f.createErr
}
func (f *fakeNotifier) UpdateNotification(_ context.Context, e sonarr.Notification, _ sonarr.NotificationPayload) (sonarr.Notification, error) {
	ee := e
	f.updateExisting = &ee
	return f.updateResp, f.updateErr
}
func (f *fakeNotifier) DeleteNotification(_ context.Context, id int) error {
	f.deleteIDs = append(f.deleteIDs, id)
	return f.deleteErr
}

func newReconciler(t *testing.T, snap runtime.InstanceSnapshot, n *fakeNotifier, publicURL string) (*Reconciler, *StatusCache) {
	t.Helper()
	cache := NewStatusCache()
	r := New(Deps{
		Lookup: func(name string) (runtime.InstanceSnapshot, SonarrNotifier, bool) {
			if name != snap.Name {
				return runtime.InstanceSnapshot{}, nil, false
			}
			return snap, n, true
		},
		PublicURL: func(context.Context) string { return publicURL },
		Cache:     cache, APIKey: "key",
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	return r, cache
}

func TestReconcile_CreateWhenMissing(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{createResp: sonarr.Notification{ID: 7, Implementation: "Webhook"}}
	r, _ := newReconciler(t, snap, n, "https://sf.example")
	st, err := r.Reconcile(context.Background(), "alpha")
	if err != nil || !st.Installed || st.NotificationID == nil || *st.NotificationID != 7 {
		t.Fatalf("unexpected: %+v err=%v", st, err)
	}
	if n.createCall == nil || n.createCall.URL != "https://sf.example/api/v1/webhook/sonarr/alpha" {
		t.Fatalf("bad create payload: %+v", n.createCall)
	}
}

func TestReconcile_UpdateWhenURLChanged(t *testing.T) {
	override := "https://new.example"
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true, WebhookURLOverride: &override}
	n := &fakeNotifier{
		list: []sonarr.Notification{{
			ID: 99, Implementation: "Webhook",
			Fields: []sonarr.NotificationField{{Name: "url", Value: "https://old.example/api/v1/webhook/sonarr/alpha"}},
		}},
		updateResp: sonarr.Notification{ID: 99, Implementation: "Webhook"},
	}
	r, _ := newReconciler(t, snap, n, "https://sf.example")
	st, err := r.Reconcile(context.Background(), "alpha")
	if err != nil || n.updateExisting == nil || *st.InstalledURL != "https://new.example/api/v1/webhook/sonarr/alpha" {
		t.Fatalf("unexpected: %+v err=%v", st, err)
	}
}

func TestReconcile_RecordsErrorOnListFailure(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{listErr: errors.New("boom")}
	r, cache := newReconciler(t, snap, n, "https://sf.example")
	if _, err := r.Reconcile(context.Background(), "alpha"); err == nil {
		t.Fatalf("expected err")
	}
	cur, _ := cache.Get("alpha")
	if cur.LastError == nil || cur.NextRetryAt == nil {
		t.Fatalf("expected LastError + NextRetryAt")
	}
}

func TestHandleInstanceDeleted_CleansSonarrAndCache(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{}
	r, cache := newReconciler(t, snap, n, "https://sf.example")
	id := 11
	cache.Set("alpha", Status{Installed: true, NotificationID: &id})
	r.HandleInstanceDeleted(context.Background(), "alpha")
	if _, ok := cache.Get("alpha"); ok {
		t.Fatalf("cache should be purged")
	}
	if len(n.deleteIDs) != 1 || n.deleteIDs[0] != 11 {
		t.Fatalf("expected DeleteNotification(11), got %v", n.deleteIDs)
	}
}

func TestReconcile_DisabledNoOp(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: false}
	n := &fakeNotifier{}
	r, cache := newReconciler(t, snap, n, "https://sf.example")
	st, err := r.Reconcile(context.Background(), "alpha")
	if err != nil || st.Installed {
		t.Fatalf("expected no-op: %+v err=%v", st, err)
	}
	cur, ok := cache.Get("alpha")
	if !ok || cur.Installed {
		t.Fatalf("expected Installed=false in cache")
	}
	if len(n.list) > 0 || n.createCall != nil {
		t.Fatalf("expected zero Sonarr calls")
	}
}

func TestReconcile_NoOpWhenURLMatches(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{
		list: []sonarr.Notification{{
			ID: 42, Implementation: "Webhook",
			Fields: []sonarr.NotificationField{
				{Name: "url", Value: "https://sf.example/api/v1/webhook/sonarr/alpha"},
			},
		}},
	}
	r, _ := newReconciler(t, snap, n, "https://sf.example")
	st, err := r.Reconcile(context.Background(), "alpha")
	if err != nil || !st.Installed || st.NotificationID == nil || *st.NotificationID != 42 {
		t.Fatalf("unexpected: %+v err=%v", st, err)
	}
	if n.updateExisting != nil || n.createCall != nil {
		t.Fatalf("expected no Create/Update call when URL matches")
	}
}

func TestReconcile_PublicURLUndetermined(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{}
	r, cache := newReconciler(t, snap, n, "")
	st, err := r.Reconcile(context.Background(), "alpha")
	if err == nil || err.Error() != "public_url undetermined" {
		t.Fatalf("expected public_url undetermined error")
	}
	if st.LastError == nil || *st.LastError != "public_url undetermined" {
		t.Fatalf("expected LastError set in cache")
	}
	cur, ok := cache.Get("alpha")
	if !ok || cur.LastError == nil {
		t.Fatalf("expected error cached")
	}
}

func TestReconcile_UnknownInstance(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{}
	r, _ := newReconciler(t, snap, n, "https://sf.example")
	_, err := r.Reconcile(context.Background(), "unknown")
	if !errors.Is(err, ErrUnknownInstance) {
		t.Fatalf("expected ErrUnknownInstance, got %v", err)
	}
}

func TestGetStatus_LazyRefreshOnEmpty(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{createResp: sonarr.Notification{ID: 77, Implementation: "Webhook"}}
	r, cache := newReconciler(t, snap, n, "https://sf.example")

	st, err := r.GetStatus(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !st.Installed || st.NotificationID == nil || *st.NotificationID != 77 {
		t.Fatalf("expected lazy refresh to trigger Create: %+v", st)
	}
	cur, ok := cache.Get("alpha")
	if !ok || cur.NotificationID == nil || *cur.NotificationID != 77 {
		t.Fatalf("expected cache populated")
	}
}

func TestGetStatus_WarmCacheServedDirect(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{}
	r, cache := newReconciler(t, snap, n, "https://sf.example")

	now := time.Now()
	id := 55
	url := "https://sf.example/api/v1/webhook/sonarr/alpha"
	cache.Set("alpha", Status{
		Installed:      true,
		NotificationID: &id,
		InstalledURL:   &url,
		LastCheckedAt:  now,
	})
	r = r.WithClock(func() time.Time { return now })

	st, err := r.GetStatus(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !st.Installed || *st.NotificationID != 55 {
		t.Fatalf("expected cached value: %+v", st)
	}
	if n.createCall != nil || len(n.list) > 0 {
		t.Fatalf("expected zero Sonarr calls (warm cache)")
	}
}

func TestGetStatus_StaleErrorTriggersRefresh(t *testing.T) {
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{createResp: sonarr.Notification{ID: 88, Implementation: "Webhook"}}
	r, cache := newReconciler(t, snap, n, "https://sf.example")

	now := time.Now()
	msg := "old error"
	past := now.Add(-10 * time.Minute)
	cache.Set("alpha", Status{
		LastError:     &msg,
		LastCheckedAt: past,
		NextRetryAt:   &past,
	})
	r = r.WithClock(func() time.Time { return now })

	st, err := r.GetStatus(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !st.Installed || st.NotificationID == nil || *st.NotificationID != 88 {
		t.Fatalf("expected refresh cleared error: %+v", st)
	}
	if st.LastError != nil {
		t.Fatalf("expected LastError cleared on successful refresh")
	}
}
