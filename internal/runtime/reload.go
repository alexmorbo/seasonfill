package runtime

import (
	"context"
	"log/slog"
	"sync"
)

// Bus fans Snapshot publishes out to named subscribers. The channel
// buffer is 1; if a subscriber is slow the older snapshot is dropped
// (latest-wins) and the drop is logged. Subscribers are responsible
// for rebuilding their own state idempotently.
type Bus struct {
	mu     sync.RWMutex
	subs   map[string]chan Snapshot
	logger *slog.Logger
	closed bool
}

func NewBus(logger *slog.Logger) *Bus {
	if logger == nil {
		logger = slog.Default()
	}
	return &Bus{subs: make(map[string]chan Snapshot), logger: logger}
}

func (b *Bus) Subscribe(name string) <-chan Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		ch := make(chan Snapshot)
		close(ch)
		return ch
	}
	if existing, ok := b.subs[name]; ok {
		close(existing)
	}
	ch := make(chan Snapshot, 1)
	b.subs[name] = ch
	return ch
}

func (b *Bus) Unsubscribe(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[name]; ok {
		delete(b.subs, name)
		close(ch)
	}
}

func (b *Bus) Publish(ctx context.Context, snap Snapshot) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for name, ch := range b.subs {
		select {
		case ch <- snap:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snap:
				b.logger.WarnContext(ctx, "reload.bus.dropped_stale",
					slog.String("subscriber", name))
			default:
				b.logger.WarnContext(ctx, "reload.bus.publish_dropped",
					slog.String("subscriber", name))
			}
		}
	}
}

func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for name, ch := range b.subs {
		close(ch)
		delete(b.subs, name)
	}
}
