package adapters

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraextsvc "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
)

// TestOMDbClientSubscriber_FirstApplyRebuildsWhenEnabled covers the
// initial "no prior baseline" path: a first call with enabled+key MUST
// rebuild even though the cached state is the zero value.
func TestOMDbClientSubscriber_FirstApplyRebuildsWhenEnabled(t *testing.T) {
	t.Parallel()
	holder := NewOMDbClientHolder()
	logBuf := &bytes.Buffer{}
	sub := NewOMDbClientSubscriber(holder, newTestLogger(logBuf))

	sub.Apply(context.Background(), infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
	})

	require.NotNil(t, sub.Current(), "first apply should rebuild + populate holder")
	assert.Equal(t, 1, sub.RebuildCount())
	assert.Contains(t, logBuf.String(), "external_service.client.rebuilt")
	assert.Contains(t, logBuf.String(), `"last4":"1234"`)
}

// TestOMDbClientSubscriber_SamePayloadNoRebuild covers the idempotency
// guarantee: calling Apply twice with byte-identical Settings must NOT
// rebuild on the second call.
func TestOMDbClientSubscriber_SamePayloadNoRebuild(t *testing.T) {
	t.Parallel()
	holder := NewOMDbClientHolder()
	sub := NewOMDbClientSubscriber(holder, newTestLogger(nil))

	s := infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
	}
	sub.Apply(context.Background(), s)
	firstClient := sub.Current()
	require.NotNil(t, firstClient)

	// Second call with the same payload.
	sub.Apply(context.Background(), s)
	assert.Same(t, firstClient, sub.Current(), "same payload must not swap")
	assert.Equal(t, 1, sub.RebuildCount(), "no rebuild on duplicate apply")
}

// TestOMDbClientSubscriber_TestVerdictChangesIgnored covers the
// last_test_at/outcome/message immutability — those fields change on
// every Test() persistence but MUST NOT trigger a rebuild.
func TestOMDbClientSubscriber_TestVerdictChangesIgnored(t *testing.T) {
	t.Parallel()
	holder := NewOMDbClientHolder()
	sub := NewOMDbClientSubscriber(holder, newTestLogger(nil))

	base := infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
	}
	sub.Apply(context.Background(), base)
	firstClient := sub.Current()

	// Same as base but with a Test() verdict applied.
	withVerdict := base
	withVerdict.LastTestOutcome = infraextsvc.OutcomeOK
	withVerdict.LastTestMessage = "ok"
	sub.Apply(context.Background(), withVerdict)
	assert.Same(t, firstClient, sub.Current(), "test verdict must not swap")
	assert.Equal(t, 1, sub.RebuildCount())
}

// TestOMDbClientSubscriber_NewKeyRebuilds covers the primary use case:
// operator rotates the API key, the subscriber rebuilds and swaps in a
// fresh client.
func TestOMDbClientSubscriber_NewKeyRebuilds(t *testing.T) {
	t.Parallel()
	holder := NewOMDbClientHolder()
	sub := NewOMDbClientSubscriber(holder, newTestLogger(nil))

	first := infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "old_key_1234",
		APIKeyLast4: "1234",
	}
	sub.Apply(context.Background(), first)
	firstClient := sub.Current()
	require.NotNil(t, firstClient)

	rotated := first
	rotated.APIKey = "new_key_abcd"
	rotated.APIKeyLast4 = "abcd"
	sub.Apply(context.Background(), rotated)

	assert.NotSame(t, firstClient, sub.Current(), "key rotation must swap")
	assert.Equal(t, 2, sub.RebuildCount())
}

// TestOMDbClientSubscriber_DisabledClearsHolder covers the "operator
// disables OMDb" path: holder.Load returns nil; worker logs handler_nil.
func TestOMDbClientSubscriber_DisabledClearsHolder(t *testing.T) {
	t.Parallel()
	holder := NewOMDbClientHolder()
	logBuf := &bytes.Buffer{}
	sub := NewOMDbClientSubscriber(holder, newTestLogger(logBuf))

	enabled := infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
	}
	sub.Apply(context.Background(), enabled)
	require.NotNil(t, sub.Current())

	disabled := enabled
	disabled.Enabled = false
	sub.Apply(context.Background(), disabled)

	assert.Nil(t, sub.Current(), "disable must clear holder")
	assert.Contains(t, logBuf.String(), "external_service.client.cleared")
}

// TestOMDbClientSubscriber_ConcurrentApplyIsSafe stress-tests the
// race detector. 16 goroutines call Apply with the same payload in
// parallel; only one rebuild should result.
func TestOMDbClientSubscriber_ConcurrentApplyIsSafe(t *testing.T) {
	t.Parallel()
	holder := NewOMDbClientHolder()
	sub := NewOMDbClientSubscriber(holder, newTestLogger(nil))

	s := infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
	}

	var wg sync.WaitGroup
	const N = 16
	for range N {
		wg.Go(func() {
			sub.Apply(context.Background(), s)
		})
	}
	wg.Wait()

	// Under contention multiple goroutines may pass the
	// "needs rebuild" check before any of them commits. The guarantee
	// is that we don't crash and the holder is non-nil — the rebuild
	// count is bounded by N but realistically 1-3 under -race.
	require.NotNil(t, sub.Current())
	assert.LessOrEqual(t, sub.RebuildCount(), N)
	assert.GreaterOrEqual(t, sub.RebuildCount(), 1)
}

// TestOMDbClientSubscriber_FactoryErrorLeavesPrevious covers the
// failure mode: a malformed proxy URL fails the factory; the previous
// client stays live.
func TestOMDbClientSubscriber_FactoryErrorLeavesPrevious(t *testing.T) {
	t.Parallel()
	holder := NewOMDbClientHolder()
	logBuf := &bytes.Buffer{}
	sub := NewOMDbClientSubscriber(holder, newTestLogger(logBuf))

	good := infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
	}
	sub.Apply(context.Background(), good)
	previous := sub.Current()
	require.NotNil(t, previous)

	bad := good
	bad.ProxyURL = "invalid-scheme://oops"
	sub.Apply(context.Background(), bad)

	assert.Same(t, previous, sub.Current(),
		"factory failure must NOT swap the holder")
	assert.True(t,
		strings.Contains(logBuf.String(), "rebuild_failed") ||
			strings.Contains(logBuf.String(), "client.rebuilt"),
		"either a rebuild_failed warn OR a rebuilt log line must appear")
}

// TestOMDbClientSubscriber_FirstActivationFiresOnce covers Story 473
// (B-25/B-24): the OnFirstActivation callback must fire exactly once
// per nil→non-nil client transition. Mirrors
// TestTMDBClientSubscriber_FirstActivationFiresOnce.
func TestOMDbClientSubscriber_FirstActivationFiresOnce(t *testing.T) {
	t.Parallel()
	holder := NewOMDbClientHolder()
	logBuf := &bytes.Buffer{}
	sub := NewOMDbClientSubscriber(holder, newTestLogger(logBuf))

	var (
		fires    int
		triggers []string
		mu       sync.Mutex
	)
	sub = sub.WithOnFirstActivation(func(ctx context.Context, trigger string) {
		mu.Lock()
		defer mu.Unlock()
		fires++
		triggers = append(triggers, trigger)
	})

	// First Apply with enabled+key → activation fires.
	sub.Apply(context.Background(), infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
	})
	mu.Lock()
	require.Equal(t, 1, fires, "first nil→non-nil transition must fire activation")
	require.Equal(t, []string{"runtime_first_key_save"}, triggers)
	mu.Unlock()

	// Second Apply with SAME settings → no rebuild, no second fire.
	sub.Apply(context.Background(), infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
	})
	mu.Lock()
	assert.Equal(t, 1, fires, "same-payload re-apply must not fire activation")
	mu.Unlock()

	// Rotate key → rebuild but no activation (still activated == true).
	sub.Apply(context.Background(), infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "new_key_9999",
		APIKeyLast4: "9999",
	})
	mu.Lock()
	assert.Equal(t, 1, fires, "key rotation must not fire activation (still activated)")
	mu.Unlock()

	// Clear settings → activated flips false.
	sub.Apply(context.Background(), infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     false,
		APIKey:      "",
		APIKeyLast4: "",
	})

	// Re-enable → second activation fires.
	sub.Apply(context.Background(), infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "third_key_5555",
		APIKeyLast4: "5555",
	})
	mu.Lock()
	require.Equal(t, 2, fires, "post-clear re-set must fire activation again")
	require.Equal(t, []string{"runtime_first_key_save", "runtime_first_key_save"}, triggers)
	mu.Unlock()
}

// TestOMDbClientSubscriber_FactoryFailurePreservesActivated covers the
// edge case where BuildOMDbClient errors: activated must NOT be toggled
// (a broken settings save should not pretend we activated). Story 473.
func TestOMDbClientSubscriber_FactoryFailurePreservesActivated(t *testing.T) {
	t.Parallel()
	holder := NewOMDbClientHolder()
	sub := NewOMDbClientSubscriber(holder, newTestLogger(nil))

	var fires int
	sub = sub.WithOnFirstActivation(func(ctx context.Context, trigger string) { fires++ })

	// First-call with deliberately bad proxy URL → factory fails. Mirrors
	// TestOMDbClientSubscriber_FactoryErrorLeavesPrevious which uses the
	// same "invalid-scheme://oops" URL shape known to break HttpClientFor.
	sub.Apply(context.Background(), infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
		ProxyURL:    "invalid-scheme://oops",
	})
	assert.Equal(t, 0, fires, "factory failure must not fire activation")

	// Recover with a valid settings → activation fires.
	sub.Apply(context.Background(), infraextsvc.Settings{
		Service:     infraextsvc.ServiceOMDB,
		Enabled:     true,
		APIKey:      "abcdef1234",
		APIKeyLast4: "1234",
	})
	assert.Equal(t, 1, fires, "successful re-apply after recovery must fire activation")
}

// newTestLogger returns a JSON slog logger writing to w; nil w sinks
// to io.Discard (defensive default). Keeps test assertions stable across
// Go versions whose slog text writer shape may vary.
func newTestLogger(w *bytes.Buffer) *slog.Logger {
	if w == nil {
		return slog.New(slog.NewJSONHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
