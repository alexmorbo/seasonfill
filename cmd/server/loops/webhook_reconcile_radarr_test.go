package loops

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/webhookinstall"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
)

// recordingReconciler records every instance name it was asked to reconcile so
// the radarr-walk tests can assert the loop dispatched the radarr name.
type recordingReconciler struct {
	mu    sync.Mutex
	names []string
}

func (r *recordingReconciler) Reconcile(_ context.Context, name string) (webhookinstall.Status, error) {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
	return webhookinstall.Status{Installed: true, LastCheckedAt: time.Now().UTC()}, nil
}

func (r *recordingReconciler) seen(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.names {
		if n == name {
			return true
		}
	}
	return false
}

func (r *recordingReconciler) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.names)
}

func (r *recordingReconciler) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.names))
	copy(out, r.names)
	return out
}

// radarrInstMap builds a single-entry radarr instance map. Client is nil — the
// loop only reads Config (runtime.InstanceSnapshot).
func radarrInstMap(name string, webhookEnabled bool) map[string]scan.RadarrInstance {
	return map[string]scan.RadarrInstance{
		name: {Config: runtime.InstanceSnapshot{
			Name: name, WebhookInstallEnabled: webhookEnabled,
		}},
	}
}

func nilSonarrLister() WebhookReconcileInstanceLister {
	return func() map[string]scan.Instance { return nil }
}

func TestWebhookReconcileLoop_WalksRadarrInstances(t *testing.T) {
	t.Parallel()
	rec := &recordingReconciler{}
	rad := radarrInstMap("movies", true)
	loop := NewWebhookReconcileLoop(rec, webhookinstall.NewStatusCache(),
		nilSonarrLister(), nullLogger()).
		WithRadarrInstances(func() map[string]scan.RadarrInstance { return rad })
	loop.SetTickInterval(40 * time.Millisecond)
	runLoopBriefly(t, loop, 150*time.Millisecond)
	if !rec.seen("movies") {
		t.Fatalf("expected radarr instance 'movies' to be reconciled; names=%v", rec.snapshot())
	}
}

func TestWebhookReconcileLoop_RadarrDisabledSkipsReconcile(t *testing.T) {
	t.Parallel()
	rec := &recordingReconciler{}
	rad := radarrInstMap("movies", false)
	loop := NewWebhookReconcileLoop(rec, webhookinstall.NewStatusCache(),
		nilSonarrLister(), nullLogger()).
		WithRadarrInstances(func() map[string]scan.RadarrInstance { return rad })
	loop.SetTickInterval(30 * time.Millisecond)
	runLoopBriefly(t, loop, 120*time.Millisecond)
	if got := rec.count(); got != 0 {
		t.Fatalf("expected 0 calls for disabled radarr instance, got %d", got)
	}
}

func TestWebhookReconcileLoop_RadarrFreshCacheSkips(t *testing.T) {
	t.Parallel()
	cache := webhookinstall.NewStatusCache()
	rec := &recordingReconciler{}
	now := time.Now().UTC()
	cache.Set("movies", webhookinstall.Status{Installed: true, LastCheckedAt: now.Add(-1 * time.Millisecond)})
	rad := radarrInstMap("movies", true)
	loop := NewWebhookReconcileLoop(rec, cache,
		nilSonarrLister(), nullLogger()).
		WithRadarrInstances(func() map[string]scan.RadarrInstance { return rad })
	loop.SetTickInterval(30 * time.Millisecond)
	loop.withClock(func() time.Time { return now })
	runLoopBriefly(t, loop, 120*time.Millisecond)
	if got := rec.count(); got != 0 {
		t.Fatalf("expected 0 calls with fresh radarr cache (idempotent), got %d", got)
	}
}

func TestWebhookReconcileLoop_WalksBothSonarrAndRadarr(t *testing.T) {
	t.Parallel()
	rec := &recordingReconciler{}
	son := instMap("series", true) // helper from webhook_reconcile_test.go
	rad := radarrInstMap("movies", true)
	loop := NewWebhookReconcileLoop(rec, webhookinstall.NewStatusCache(),
		func() map[string]scan.Instance { return son }, nullLogger()).
		WithRadarrInstances(func() map[string]scan.RadarrInstance { return rad })
	loop.SetTickInterval(40 * time.Millisecond)
	runLoopBriefly(t, loop, 150*time.Millisecond)
	if !rec.seen("series") {
		t.Fatalf("sonarr instance not reconciled (regression); names=%v", rec.snapshot())
	}
	if !rec.seen("movies") {
		t.Fatalf("radarr instance not reconciled; names=%v", rec.snapshot())
	}
}

// TestWebhookReconcileLoop_NilRadarrListerSonarrOnly is the byte-for-byte guard:
// with no radarr lister the loop reconciles ONLY sonarr names, exactly as before
// R-6, and never panics on the nil radarr source.
func TestWebhookReconcileLoop_NilRadarrListerSonarrOnly(t *testing.T) {
	t.Parallel()
	rec := &recordingReconciler{}
	son := instMap("series", true)
	loop := NewWebhookReconcileLoop(rec, webhookinstall.NewStatusCache(),
		func() map[string]scan.Instance { return son }, nullLogger()).
		WithRadarrInstances(nil)
	loop.SetTickInterval(40 * time.Millisecond)
	runLoopBriefly(t, loop, 150*time.Millisecond)
	if !rec.seen("series") {
		t.Fatalf("sonarr instance not reconciled with nil radarr lister; names=%v", rec.snapshot())
	}
	for _, n := range rec.snapshot() {
		if n != "series" {
			t.Fatalf("unexpected non-sonarr reconcile %q with nil radarr lister", n)
		}
	}
}

// --- end-to-end: loop -> REAL reconciler -> fake radarr notifier ---

// fakeRadarrNotifier is a minimal webhookinstall.RadarrNotifier. ListNotifications
// returns whatever was created so the reconciler's second pass finds a match and
// no-ops (idempotency), and CreateNotification records the URL so the test can
// assert the canonical /webhook/radarr/ path.
type fakeRadarrNotifier struct {
	mu            sync.Mutex
	created       []radarr.Notification
	createCalls   int
	updateCalls   int
	lastCreateURL string
}

func (f *fakeRadarrNotifier) ListNotifications(context.Context) ([]radarr.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]radarr.Notification, len(f.created))
	copy(out, f.created)
	return out, nil
}

func (f *fakeRadarrNotifier) CreateNotification(_ context.Context, p radarr.NotificationPayload) (radarr.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	f.lastCreateURL = p.URL
	n := radarr.Notification{
		ID:             7,
		Implementation: "Webhook",
		Fields:         []radarr.NotificationField{{Name: "url", Value: p.URL}},
	}
	f.created = append(f.created, n)
	return n, nil
}

func (f *fakeRadarrNotifier) UpdateNotification(_ context.Context, existing radarr.Notification, _ radarr.NotificationPayload) (radarr.Notification, error) {
	f.mu.Lock()
	f.updateCalls++
	f.mu.Unlock()
	return existing, nil
}

func (f *fakeRadarrNotifier) TestNotification(context.Context, radarr.NotificationPayload) error {
	return nil
}

func (f *fakeRadarrNotifier) DeleteNotification(context.Context, int) error { return nil }

func (f *fakeRadarrNotifier) counts() (create, update int, url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls, f.updateCalls, f.lastCreateURL
}

// TestWebhookReconcileLoop_Radarr_EndToEnd_CanonicalPathAndIdempotent drives the
// loop into a REAL reconciler whose radarrLookup returns a fake notifier. It
// proves, in one pass: (a) the loop walks radarr, (b) the reconciler installs
// the canonical .../api/v1/webhook/radarr/movies URL, (c) it is idempotent —
// exactly one Create across many ticks (subsequent ticks find the match and
// no-op via fresh-cache skip + reconciler match path).
func TestWebhookReconcileLoop_Radarr_EndToEnd_CanonicalPathAndIdempotent(t *testing.T) {
	t.Parallel()

	notifier := &fakeRadarrNotifier{}
	cache := webhookinstall.NewStatusCache()

	reconciler := webhookinstall.New(webhookinstall.Deps{
		// Sonarr lookup always misses "movies" so the radarr branch fires.
		Lookup: func(string) (runtime.InstanceSnapshot, webhookinstall.SonarrNotifier, bool) {
			return runtime.InstanceSnapshot{}, nil, false
		},
		RadarrLookup: func(name string) (runtime.InstanceSnapshot, webhookinstall.RadarrNotifier, bool) {
			if name != "movies" {
				return runtime.InstanceSnapshot{}, nil, false
			}
			return runtime.InstanceSnapshot{Name: "movies", WebhookInstallEnabled: true}, notifier, true
		},
		PublicURL: func(context.Context) string { return "https://sf.example" },
		Cache:     cache,
		APIKey:    "k",
		Logger:    nullLogger(),
	})

	rad := radarrInstMap("movies", true)
	loop := NewWebhookReconcileLoop(reconciler, cache,
		nilSonarrLister(), nullLogger()).
		WithRadarrInstances(func() map[string]scan.RadarrInstance { return rad })
	loop.SetTickInterval(30 * time.Millisecond)
	runLoopBriefly(t, loop, 200*time.Millisecond)

	create, update, url := notifier.counts()
	if create != 1 {
		t.Fatalf("expected exactly 1 CreateNotification (idempotent across ticks), got %d", create)
	}
	if update != 0 {
		t.Fatalf("expected 0 UpdateNotification on a converged webhook, got %d", update)
	}
	const want = "https://sf.example/api/v1/webhook/radarr/movies"
	if url != want {
		t.Fatalf("canonical radarr path mismatch: got %q want %q", url, want)
	}
	if !strings.HasSuffix(url, "/api/v1/webhook/radarr/movies") {
		t.Fatalf("expected canonical /webhook/radarr/ suffix, got %q", url)
	}
}
