package webhookinstall

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
)

// fakeRadarrNotifier is the radarr twin of fakeNotifier. syncListOnCreate /
// syncListOnUpdate emulate Radarr persisting exactly what it accepted, so the
// NEXT reconcile's LIST sees the achieved set (idempotency / storm assertions).
type fakeRadarrNotifier struct {
	list             []radarr.Notification
	listErr          error
	createCall       *radarr.NotificationPayload
	createResp       radarr.Notification
	createErr        error
	createCalls      int
	updateExisting   *radarr.Notification
	updateResp       radarr.Notification
	updateErr        error
	updateCalls      int
	testCall         *radarr.NotificationPayload
	testCalls        int
	testFn           func(radarr.NotificationPayload) error
	syncListOnCreate bool
	syncListOnUpdate bool
	deleteIDs        []int
	deleteErr        error
}

func (f *fakeRadarrNotifier) ListNotifications(context.Context) ([]radarr.Notification, error) {
	return f.list, f.listErr
}

func (f *fakeRadarrNotifier) CreateNotification(_ context.Context, p radarr.NotificationPayload) (radarr.Notification, error) {
	pp := p
	f.createCall = &pp
	f.createCalls++
	if f.createErr == nil && f.syncListOnCreate {
		f.list = []radarr.Notification{{
			ID:                f.createResp.ID,
			Implementation:    "Webhook",
			OnGrab:            f.createResp.OnGrab,
			OnDownload:        f.createResp.OnDownload,
			OnMovieAdded:      f.createResp.OnMovieAdded,
			OnMovieDelete:     f.createResp.OnMovieDelete,
			OnMovieFileDelete: f.createResp.OnMovieFileDelete,
			OnRename:          f.createResp.OnRename,
			Fields:            []radarr.NotificationField{{Name: "url", Value: p.URL}},
		}}
	}
	return f.createResp, f.createErr
}

func (f *fakeRadarrNotifier) UpdateNotification(_ context.Context, e radarr.Notification, p radarr.NotificationPayload) (radarr.Notification, error) {
	ee := e
	f.updateExisting = &ee
	f.updateCalls++
	if f.updateErr == nil && f.syncListOnUpdate {
		f.list = []radarr.Notification{{
			ID:                f.updateResp.ID,
			Implementation:    "Webhook",
			OnGrab:            f.updateResp.OnGrab,
			OnDownload:        f.updateResp.OnDownload,
			OnMovieAdded:      f.updateResp.OnMovieAdded,
			OnMovieDelete:     f.updateResp.OnMovieDelete,
			OnMovieFileDelete: f.updateResp.OnMovieFileDelete,
			OnRename:          f.updateResp.OnRename,
			Fields:            []radarr.NotificationField{{Name: "url", Value: p.URL}},
		}}
	}
	return f.updateResp, f.updateErr
}

func (f *fakeRadarrNotifier) TestNotification(_ context.Context, p radarr.NotificationPayload) error {
	pp := p
	f.testCall = &pp
	f.testCalls++
	if f.testFn != nil {
		return f.testFn(p)
	}
	return nil
}

func (f *fakeRadarrNotifier) DeleteNotification(_ context.Context, id int) error {
	f.deleteIDs = append(f.deleteIDs, id)
	return f.deleteErr
}

// newRadarrReconciler wires a Reconciler whose sonarr Lookup always misses
// (ok=false) and whose RadarrLookup resolves the fake — exercising the radarr
// fallback branch. Deps.Lookup must be non-nil (New panics otherwise).
func newRadarrReconciler(t *testing.T, snap runtime.InstanceSnapshot, n *fakeRadarrNotifier, publicURL string) (*Reconciler, *StatusCache) {
	t.Helper()
	cache := NewStatusCache()
	r := New(Deps{
		Lookup: func(string) (runtime.InstanceSnapshot, SonarrNotifier, bool) {
			return runtime.InstanceSnapshot{}, nil, false
		},
		RadarrLookup: func(name string) (runtime.InstanceSnapshot, RadarrNotifier, bool) {
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

// fullRadarrDesired is the trigger set a converged radarr notification carries.
func fullRadarrDesired() radarr.Notification {
	return radarr.Notification{
		OnGrab: true, OnDownload: true, OnMovieAdded: true,
		OnMovieDelete: true, OnMovieFileDelete: true, OnRename: true,
	}
}

func TestReconcileRadarr_CreateWhenMissing(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "rad", Type: "radarr", WebhookInstallEnabled: true}
	cr := fullRadarrDesired()
	cr.ID = 7
	cr.Implementation = "Webhook"
	n := &fakeRadarrNotifier{createResp: cr}
	r, _ := newRadarrReconciler(t, snap, n, "https://sf.example")

	st, err := r.Reconcile(context.Background(), "rad")
	if err != nil || !st.Installed || st.NotificationID == nil || *st.NotificationID != 7 {
		t.Fatalf("unexpected: %+v err=%v", st, err)
	}
	if n.createCall == nil || n.createCall.URL != "https://sf.example/api/v1/webhook/radarr/rad" {
		t.Fatalf("bad create payload (must use radarr canonical path): %+v", n.createCall)
	}
	if n.testCalls != 1 {
		t.Fatalf("expected exactly one TestNotification, got %d", n.testCalls)
	}
	if st.InstalledURL == nil || *st.InstalledURL != "https://sf.example/api/v1/webhook/radarr/rad" {
		t.Fatalf("InstalledURL must be the radarr canonical path: %+v", st.InstalledURL)
	}
}

func TestReconcileRadarr_IdempotentNoChurn(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "rad", Type: "radarr", WebhookInstallEnabled: true}
	cr := fullRadarrDesired()
	cr.ID = 7
	cr.Implementation = "Webhook"
	// syncListOnCreate: after Create, the fake's LIST returns the created
	// webhook (full desired triggers + canonical URL), so the 2nd reconcile
	// sees URL match + trigger convergence → no Create/Update.
	n := &fakeRadarrNotifier{createResp: cr, syncListOnCreate: true}
	r, _ := newRadarrReconciler(t, snap, n, "https://sf.example")

	if _, err := r.Reconcile(context.Background(), "rad"); err != nil {
		t.Fatalf("tick1 err: %v", err)
	}
	if n.createCalls != 1 {
		t.Fatalf("tick1 expected exactly one Create, got %d", n.createCalls)
	}
	// 2nd consecutive reconcile: webhook present, URL matches, triggers match
	// desired → idempotent, zero writes.
	st, err := r.Reconcile(context.Background(), "rad")
	if err != nil || !st.Installed {
		t.Fatalf("tick2 unexpected: %+v err=%v", st, err)
	}
	if n.createCalls != 1 {
		t.Fatalf("retry storm: Create must stay at 1, got %d", n.createCalls)
	}
	if n.updateCalls != 0 {
		t.Fatalf("idempotent tick must not Update, got %d", n.updateCalls)
	}
}

func TestReconcileRadarr_UpdateWhenTriggersDrift(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "rad", Type: "radarr", WebhookInstallEnabled: true}
	// URL already matches, but onRename is OFF (the alignment gap) → drift vs
	// the ideal desired set.
	n := &fakeRadarrNotifier{
		list: []radarr.Notification{{
			ID: 42, Implementation: "Webhook",
			OnGrab: true, OnDownload: true, OnMovieAdded: true,
			OnMovieDelete: true, OnMovieFileDelete: true, OnRename: false,
			Fields: []radarr.NotificationField{
				{Name: "url", Value: "https://sf.example/api/v1/webhook/radarr/rad"},
			},
		}},
		updateResp: func() radarr.Notification {
			u := fullRadarrDesired()
			u.ID = 42
			u.Implementation = "Webhook"
			return u
		}(),
		syncListOnUpdate: true,
	}
	r, _ := newRadarrReconciler(t, snap, n, "https://sf.example")

	// tick1: drift (onRename missing) → exactly one Update; memo := achieved.
	st, err := r.Reconcile(context.Background(), "rad")
	if err != nil || !st.Installed || st.NotificationID == nil || *st.NotificationID != 42 {
		t.Fatalf("tick1 unexpected: %+v err=%v", st, err)
	}
	if n.updateCalls != 1 {
		t.Fatalf("tick1 expected exactly one Update on trigger drift, got %d", n.updateCalls)
	}
	// tick2: LIST now returns the achieved (full) set → converged → no Update.
	if _, err := r.Reconcile(context.Background(), "rad"); err != nil {
		t.Fatalf("tick2 err: %v", err)
	}
	if n.updateCalls != 1 {
		t.Fatalf("retry storm: Update must stay at 1 after convergence, got %d", n.updateCalls)
	}
}

func TestReconcileRadarr_CreateTestFailsBlocksInstalled(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "rad", Type: "radarr", WebhookInstallEnabled: true}
	cr := fullRadarrDesired()
	cr.ID = 7
	cr.Implementation = "Webhook"
	n := &fakeRadarrNotifier{
		createResp: cr,
		testFn:     func(radarr.NotificationPayload) error { return context.DeadlineExceeded },
	}
	r, cache := newRadarrReconciler(t, snap, n, "https://sf.example")

	st, err := r.Reconcile(context.Background(), "rad")
	if err == nil {
		t.Fatalf("expected error when the radarr test round-trip fails")
	}
	if st.Installed {
		t.Fatalf("webhook must NOT be Installed when Radarr cannot deliver: %+v", st)
	}
	if st.LastError == nil || st.NextRetryAt == nil {
		t.Fatalf("expected LastError + NextRetryAt on test failure: %+v", st)
	}
	cur, _ := cache.Get("rad")
	if cur.Installed {
		t.Fatalf("cache must not carry Installed=true after a failed test")
	}
}

func TestReconcileRadarr_HandleInstanceDeletedCleansRadarr(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "rad", Type: "radarr", WebhookInstallEnabled: true}
	n := &fakeRadarrNotifier{}
	r, cache := newRadarrReconciler(t, snap, n, "https://sf.example")
	id := 21
	cache.Set("rad", Status{Installed: true, NotificationID: &id})

	r.HandleInstanceDeleted(context.Background(), "rad")
	if _, ok := cache.Get("rad"); ok {
		t.Fatalf("cache should be purged")
	}
	if len(n.deleteIDs) != 1 || n.deleteIDs[0] != 21 {
		t.Fatalf("expected radarr DeleteNotification(21), got %v", n.deleteIDs)
	}
}
