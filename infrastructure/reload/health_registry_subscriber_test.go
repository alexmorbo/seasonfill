package reload

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

type fakeChecker struct {
	mu       sync.Mutex
	calls    int32
	lastList []ports.SonarrClient
}

func (f *fakeChecker) ReplaceClients(c []ports.SonarrClient) {
	f.mu.Lock()
	defer f.mu.Unlock()
	atomic.AddInt32(&f.calls, 1)
	f.lastList = append([]ports.SonarrClient(nil), c...)
}

func (f *fakeChecker) Calls() int32 { return atomic.LoadInt32(&f.calls) }

func (f *fakeChecker) Last() []ports.SonarrClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ports.SonarrClient(nil), f.lastList...)
}

func TestHealthRegistrySubscriber_ReplaysOnEverySnapshot(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()

	checker := &fakeChecker{}
	current := []ports.SonarrClient{newFakeClient("alpha")}
	sub := NewHealthRegistrySubscriber(checker, func() []ports.SonarrClient { return current }, slog.Default())
	go sub.Run(ctx, bus)
	time.Sleep(10 * time.Millisecond)

	bus.Publish(ctx, runtime.Snapshot{})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && checker.Calls() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(1), checker.Calls(), "first publish must trigger one ReplaceClients")
	assert.Len(t, checker.Last(), 1)

	// Add beta to the lister, publish again.
	current = []ports.SonarrClient{newFakeClient("alpha"), newFakeClient("beta")}
	bus.Publish(ctx, runtime.Snapshot{})
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && checker.Calls() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(2), checker.Calls())
	assert.Len(t, checker.Last(), 2)
}
