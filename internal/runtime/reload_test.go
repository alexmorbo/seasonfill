package runtime

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBus_Subscribe_PublishFanOut(t *testing.T) {
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
	bus := NewBus(slog.Default())
	bus.Close()
	bus.Close()
}

func TestBus_SubscribeAfterClose(t *testing.T) {
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
	bus := NewBus(nil)
	require.NotNil(t, bus)
	defer bus.Close()
}
