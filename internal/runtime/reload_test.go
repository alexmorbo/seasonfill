package runtime

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBus_Subscribe_PublishFanOut(t *testing.T) {
	t.Parallel()
	bus := NewBus(slog.Default())
	defer bus.Close()

	ch1 := bus.Subscribe("sub1")
	ch2 := bus.Subscribe("sub2")

	snap := Snapshot{DryRun: true}
	bus.Publish(context.Background(), snap)

	select {
	case msg := <-ch1:
		assert.True(t, msg.DryRun)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch1 did not receive publish")
	}

	select {
	case msg := <-ch2:
		assert.True(t, msg.DryRun)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch2 did not receive publish")
	}
}

func TestBus_Publish_NonBlocking(t *testing.T) {
	t.Parallel()
	bus := NewBus(slog.Default())
	defer bus.Close()

	ch := bus.Subscribe("slow")

	snap := Snapshot{DryRun: true}
	bus.Publish(context.Background(), snap)

	time.Sleep(10 * time.Millisecond)

	bus.Publish(context.Background(), snap)

	received := <-ch
	assert.True(t, received.DryRun)
}

func TestBus_Unsubscribe(t *testing.T) {
	t.Parallel()
	bus := NewBus(slog.Default())
	defer bus.Close()

	ch := bus.Subscribe("test")
	bus.Unsubscribe("test")

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel should be closed")
	}
}

func TestBus_Close(t *testing.T) {
	t.Parallel()
	bus := NewBus(slog.Default())

	ch := bus.Subscribe("test")
	bus.Close()

	snap := Snapshot{DryRun: true}
	bus.Publish(context.Background(), snap)

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	default:
		t.Fatal("channel should be closed")
	}
}

func TestBus_CloseIdempotent(t *testing.T) {
	t.Parallel()
	bus := NewBus(slog.Default())
	bus.Close()
	bus.Close()
}

func TestBus_SubscribeAfterClose(t *testing.T) {
	t.Parallel()
	bus := NewBus(slog.Default())
	bus.Close()

	ch := bus.Subscribe("test")

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel should be closed immediately")
	}
}

func TestBus_ResubscribeClosesOld(t *testing.T) {
	t.Parallel()
	bus := NewBus(slog.Default())
	defer bus.Close()

	ch1 := bus.Subscribe("test")

	ch2 := bus.Subscribe("test")

	select {
	case _, ok := <-ch1:
		assert.False(t, ok, "old channel should be closed")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("old channel should be closed")
	}

	snap := Snapshot{DryRun: true}
	bus.Publish(context.Background(), snap)

	select {
	case msg := <-ch2:
		assert.True(t, msg.DryRun)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("new channel should receive publish")
	}
}

func TestBus_NilLogger(t *testing.T) {
	t.Parallel()
	bus := NewBus(nil)
	require.NotNil(t, bus)
	bus.Close()
}

func TestBus_Subscribe_OnReadyFiresBeforeReturn(t *testing.T) {
	t.Parallel()
	bus := NewBus(slog.Default())
	defer bus.Close()

	var fired atomic.Bool
	ch := bus.Subscribe("test", WithReady(func() {
		fired.Store(true)
	}))
	// The hook must have run by the time Subscribe returns.
	assert.True(t, fired.Load(), "WithReady hook must fire before Subscribe returns")
	_ = ch
}

func TestBus_Subscribe_OnReadyFiresAfterRegistration(t *testing.T) {
	t.Parallel()
	bus := NewBus(slog.Default())
	defer bus.Close()

	// Inside the hook, the channel MUST already be registered — a
	// concurrent Publish must reach it. We prove this by publishing
	// from inside the hook (on a fresh goroutine so we don't
	// deadlock on bus.mu) and observing the message on the channel.
	var (
		published = make(chan struct{})
		hookDone  = make(chan struct{})
	)
	ch := bus.Subscribe("test", WithReady(func() {
		go func() {
			bus.Publish(context.Background(), Snapshot{DryRun: true})
			close(published)
		}()
		<-published
		close(hookDone)
	}))
	<-hookDone

	select {
	case msg := <-ch:
		assert.True(t, msg.DryRun, "channel registered before hook fired must receive publish")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("publish from inside WithReady hook did not reach the subscribed channel")
	}
}

func TestBus_Subscribe_NoOptionPreservesBehaviour(t *testing.T) {
	t.Parallel()
	// Belt-and-suspenders: the zero-option call must behave exactly
	// like the original Subscribe(name).
	bus := NewBus(slog.Default())
	defer bus.Close()

	ch := bus.Subscribe("test")
	bus.Publish(context.Background(), Snapshot{DryRun: true})

	select {
	case msg := <-ch:
		assert.True(t, msg.DryRun)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscribe-without-options must behave identically to the original API")
	}
}
