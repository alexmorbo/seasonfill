package webhookinstall

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
)

func TestPublicURLWithFallback_ContextValueWins(t *testing.T) {
	t.Parallel()
	f := PublicURLWithFallback("https://configured.example")
	ctx := context.WithValue(context.Background(), RequestPublicURLKey{}, "https://request.example")
	if got := f(ctx); got != "https://request.example" {
		t.Fatalf("expected context value to win, got %q", got)
	}
}

func TestPublicURLWithFallback_EmptyContextUsesConfigured(t *testing.T) {
	t.Parallel()
	f := PublicURLWithFallback("https://configured.example")
	// Background context carries no RequestPublicURLKey — mirrors the
	// context-less background/pod-restart reconcile.
	if got := f(context.Background()); got != "https://configured.example" {
		t.Fatalf("expected configured fallback, got %q", got)
	}
}

func TestPublicURLWithFallback_AllEmptyReturnsEmpty(t *testing.T) {
	t.Parallel()
	f := PublicURLWithFallback("")
	if got := f(context.Background()); got != "" {
		t.Fatalf("expected empty when nothing configured, got %q", got)
	}
}

// End-to-end: a context-less reconcile with no per-instance override but
// a configured base URL must install the webhook (resolve to configured)
// instead of failing with the "public_url undetermined" sentinel.
func TestReconcile_ContextLessResolvesConfiguredFallback(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{createResp: sonarr.Notification{ID: 7, Implementation: "Webhook"}}
	cache := NewStatusCache()
	r := New(Deps{
		Lookup: func(name string) (runtime.InstanceSnapshot, SonarrNotifier, bool) {
			if name != snap.Name {
				return runtime.InstanceSnapshot{}, nil, false
			}
			return snap, n, true
		},
		PublicURL: PublicURLWithFallback("https://configured.example"),
		Cache:     cache, APIKey: "key",
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	st, err := r.Reconcile(context.Background(), "alpha")
	if err != nil || !st.Installed || st.NotificationID == nil || *st.NotificationID != 7 {
		t.Fatalf("unexpected: %+v err=%v", st, err)
	}
	if n.createCall == nil || n.createCall.URL != "https://configured.example/api/v1/webhook/sonarr/alpha" {
		t.Fatalf("bad create payload: %+v", n.createCall)
	}
}

// All three sources empty (no override, no context, empty configured
// fallback) must preserve the load-bearing sentinel unchanged.
func TestReconcile_ContextLessAllEmptyStillUndetermined(t *testing.T) {
	t.Parallel()
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{}
	cache := NewStatusCache()
	r := New(Deps{
		Lookup: func(name string) (runtime.InstanceSnapshot, SonarrNotifier, bool) {
			if name != snap.Name {
				return runtime.InstanceSnapshot{}, nil, false
			}
			return snap, n, true
		},
		PublicURL: PublicURLWithFallback(""),
		Cache:     cache, APIKey: "key",
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	st, err := r.Reconcile(context.Background(), "alpha")
	if err == nil || err.Error() != "public_url undetermined" {
		t.Fatalf("expected public_url undetermined error, got %+v err=%v", st, err)
	}
	if st.LastError == nil || *st.LastError != "public_url undetermined" {
		t.Fatalf("expected LastError sentinel in status")
	}
}
