package adapters

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	infraextsvc "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
)

// TestExternalServicesSubscriber_RegisterListener_FiresAfterApply
// verifies the Story 352 listener fan-out: a registered listener fires
// for the correct service AND only that service.
func TestExternalServicesSubscriber_RegisterListener_FiresAfterApply(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sub := NewExternalServicesSubscriber(nil, logger)

	var (
		omdbCalls atomic.Int32
		tmdbCalls atomic.Int32
		mu        sync.Mutex
		omdbSeen  infraextsvc.Settings
	)
	sub.RegisterListener(infraextsvc.ServiceOMDB, func(ctx context.Context, s infraextsvc.Settings) {
		omdbCalls.Add(1)
		mu.Lock()
		omdbSeen = s
		mu.Unlock()
	})
	sub.RegisterListener(infraextsvc.ServiceTMDB, func(ctx context.Context, s infraextsvc.Settings) {
		tmdbCalls.Add(1)
	})

	// Simulate an apply by directly calling the unexported method via
	// the bus-less Publish/test seam: prime current with hand-built
	// values then re-publish.
	sub.SetCurrentForTest(map[infraextsvc.Service]infraextsvc.Settings{
		infraextsvc.ServiceOMDB: {Service: infraextsvc.ServiceOMDB, Enabled: true, APIKey: "k_omdb"},
		infraextsvc.ServiceTMDB: {Service: infraextsvc.ServiceTMDB, Enabled: true, APIKey: "k_tmdb"},
	})
	sub.FanOutForTest(context.Background())

	assert.Equal(t, int32(1), omdbCalls.Load())
	assert.Equal(t, int32(1), tmdbCalls.Load())
	mu.Lock()
	assert.Equal(t, "k_omdb", omdbSeen.APIKey)
	mu.Unlock()
}

// TestExternalServicesSubscriber_RegisterListener_NilFnIsDropped
// guards against a nil callback panicking the apply path.
func TestExternalServicesSubscriber_RegisterListener_NilFnIsDropped(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sub := NewExternalServicesSubscriber(nil, logger)
	// Should be a no-op.
	sub.RegisterListener(infraextsvc.ServiceOMDB, nil)
	sub.SetCurrentForTest(map[infraextsvc.Service]infraextsvc.Settings{
		infraextsvc.ServiceOMDB: {Service: infraextsvc.ServiceOMDB},
	})
	// Just check no panic.
	sub.FanOutForTest(context.Background())
}
