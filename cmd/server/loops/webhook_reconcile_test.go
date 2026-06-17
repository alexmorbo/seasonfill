package loops

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// fakeWebhookReconciler: minimum surface; atomic counter for race-safe
// reads. setError is safe under -race.
type fakeWebhookReconciler struct {
	calls atomic.Int64
	mu    sync.Mutex
	err   error
}

func (f *fakeWebhookReconciler) Reconcile(_ context.Context, _ string) (webhookinstall.Status, error) {
	f.calls.Add(1)
	f.mu.Lock()
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return webhookinstall.Status{}, err
	}
	return webhookinstall.Status{Installed: true, LastCheckedAt: time.Now().UTC()}, nil
}

func (f *fakeWebhookReconciler) setError(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

// instMap builds a single-entry instance map. scan.Instance.Client is
// nil — the loop only reads Config.
func instMap(name string, webhookEnabled bool) map[string]scan.Instance {
	return map[string]scan.Instance{
		name: {Config: runtime.InstanceSnapshot{
			Name: name, WebhookInstallEnabled: webhookEnabled,
		}},
	}
}

func runLoopBriefly(t *testing.T, l *WebhookReconcileLoop, dur time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); l.Run(ctx) }()
	time.Sleep(dur)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit on cancel")
	}
}

func newTestLoop(t *testing.T, cache *webhookinstall.StatusCache, rec WebhookReconcileReconciler, instances map[string]scan.Instance) *WebhookReconcileLoop {
	t.Helper()
	return NewWebhookReconcileLoop(rec, cache,
		func() map[string]scan.Instance { return instances }, nullLogger())
}

func TestWebhookReconcileLoop_StaleCacheTriggersReconcile(t *testing.T) {
	t.Parallel()
	rec := &fakeWebhookReconciler{}
	loop := newTestLoop(t, webhookinstall.NewStatusCache(), rec, instMap("alpha", true))
	loop.SetTickInterval(40 * time.Millisecond)
	runLoopBriefly(t, loop, 150*time.Millisecond)
	if rec.calls.Load() == 0 {
		t.Fatal("expected Reconcile call with empty cache")
	}
}

func TestWebhookReconcileLoop_FreshCacheSkipsReconcile(t *testing.T) {
	t.Parallel()
	cache := webhookinstall.NewStatusCache()
	rec := &fakeWebhookReconciler{}
	now := time.Now().UTC()
	cache.Set("alpha", webhookinstall.Status{Installed: true, LastCheckedAt: now.Add(-1 * time.Millisecond)})

	loop := newTestLoop(t, cache, rec, instMap("alpha", true))
	loop.SetTickInterval(30 * time.Millisecond)
	loop.withClock(func() time.Time { return now })
	runLoopBriefly(t, loop, 120*time.Millisecond)
	if got := rec.calls.Load(); got != 0 {
		t.Fatalf("expected 0 calls with fresh cache, got %d", got)
	}
}

func TestWebhookReconcileLoop_DisabledInstanceSkipsReconcile(t *testing.T) {
	t.Parallel()
	rec := &fakeWebhookReconciler{}
	loop := newTestLoop(t, webhookinstall.NewStatusCache(), rec, instMap("alpha", false))
	loop.SetTickInterval(30 * time.Millisecond)
	runLoopBriefly(t, loop, 120*time.Millisecond)
	if got := rec.calls.Load(); got != 0 {
		t.Fatalf("expected 0 calls for disabled instance, got %d", got)
	}
}

func TestWebhookReconcileLoop_BackoffSkipsReconcile(t *testing.T) {
	t.Parallel()
	cache := webhookinstall.NewStatusCache()
	rec := &fakeWebhookReconciler{}
	now := time.Now().UTC()
	msg := "list_notifications: connection refused"
	future := now.Add(10 * time.Minute)
	cache.Set("alpha", webhookinstall.Status{
		LastError: &msg, LastCheckedAt: now.Add(-time.Minute), NextRetryAt: &future,
	})
	loop := newTestLoop(t, cache, rec, instMap("alpha", true))
	loop.SetTickInterval(30 * time.Millisecond)
	loop.withClock(func() time.Time { return now })
	runLoopBriefly(t, loop, 120*time.Millisecond)
	if got := rec.calls.Load(); got != 0 {
		t.Fatalf("expected 0 calls in backoff, got %d", got)
	}
}

func TestWebhookReconcileLoop_ReconcileErrorDoesNotKillLoop(t *testing.T) {
	t.Parallel()
	rec := &fakeWebhookReconciler{}
	rec.setError(errors.New("sonarr unreachable"))
	loop := newTestLoop(t, webhookinstall.NewStatusCache(), rec, instMap("alpha", true))
	loop.SetTickInterval(30 * time.Millisecond)
	runLoopBriefly(t, loop, 150*time.Millisecond)
	if got := rec.calls.Load(); got < 2 {
		t.Fatalf("expected loop to survive errors, got %d calls", got)
	}
}

func TestWebhookReconcileLoop_SetTickIntervalChangesCadence(t *testing.T) {
	t.Parallel()
	rec := &fakeWebhookReconciler{}
	instances := instMap("alpha", true)
	loop := NewWebhookReconcileLoop(rec, webhookinstall.NewStatusCache(),
		func() map[string]scan.Instance { return instances }, nullLogger())
	loop.SetTickInterval(200 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); loop.Run(ctx) }()

	// 150ms in: 0 ticks expected (first tick at t=200ms).
	time.Sleep(150 * time.Millisecond)
	if got := rec.calls.Load(); got > 1 {
		t.Fatalf("expected <=1 tick before SetTickInterval, got %d", got)
	}
	loop.SetTickInterval(30 * time.Millisecond)
	start := rec.calls.Load()
	time.Sleep(200 * time.Millisecond)
	if delta := rec.calls.Load() - start; delta < 3 {
		t.Fatalf("expected >=3 ticks at 30ms, got %d", delta)
	}
	cancel()
	<-done
}

func TestWebhookReconcileLoop_ZeroIntervalFallsBackToDefault(t *testing.T) {
	t.Parallel()
	loop := NewWebhookReconcileLoop(&fakeWebhookReconciler{}, webhookinstall.NewStatusCache(),
		func() map[string]scan.Instance { return nil }, nullLogger())
	loop.SetTickInterval(0)
	if got := loop.TickInterval(); got != defaultWebhookReconcileTickInterval {
		t.Fatalf("expected default %v, got %v", defaultWebhookReconcileTickInterval, got)
	}
}
