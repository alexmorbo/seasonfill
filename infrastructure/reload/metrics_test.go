package reload

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func TestRunLoop_AppliesAndCountsSuccess(t *testing.T) {
	t.Parallel()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	ctx := t.Context()
	var applied atomic.Int32
	ready := make(chan struct{})
	go runLoop(ctx, bus, "scheduler", slog.Default(),
		func(_ context.Context, _ runtime.Snapshot) error {
			applied.Add(1)
			return nil
		}, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("runLoop failed to register within 1s")
	}
	bus.Publish(ctx, runtime.Snapshot{})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && applied.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	require.GreaterOrEqual(t, applied.Load(), int32(1))
}

func TestRunLoop_ApplyErrorDoesNotCrash(t *testing.T) {
	t.Parallel()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	ctx := t.Context()
	var calls atomic.Int32
	ready := make(chan struct{})
	go runLoop(ctx, bus, "scheduler", slog.Default(),
		func(_ context.Context, _ runtime.Snapshot) error {
			calls.Add(1)
			return errors.New("boom")
		}, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("runLoop failed to register within 1s")
	}
	bus.Publish(ctx, runtime.Snapshot{})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && calls.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	require.GreaterOrEqual(t, calls.Load(), int32(1))
}
