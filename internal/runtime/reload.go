package runtime

import (
	"context"
	"log/slog"
	"sync"
)

// Bus is a single-producer-multi-consumer reload event bus with
// buffer=1 latest-wins drop-stale semantics. Each subscriber sees
// the most-recently-published snapshot; if multiple Publish calls
// land while a subscriber's apply is in flight, intermediate
// snapshots are squashed — only the latest survives. Subscribe
// returns a 1-buffered channel that callers must drain to avoid
// blocking the publisher.
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

// SubscribeOption configures an individual Subscribe call. The zero
// option set keeps Subscribe's original behaviour, so existing
// callers compile unchanged.
type SubscribeOption func(*subscribeOptions)

type subscribeOptions struct {
	onReady func()
}

// WithReady runs `fn` synchronously after the new channel has been
// registered in the bus but BEFORE Subscribe returns. The hook fires
// outside the bus mutex; it MUST NOT call back into the bus or it
// will deadlock against a concurrent Subscribe/Unsubscribe.
//
// Used by cmd/server to barrier the boot publish: each subscriber
// passes WithReady(func() { close(ready) }) so the launcher can wait
// on all six channels before issuing the first Publish.
func WithReady(fn func()) SubscribeOption {
	return func(o *subscribeOptions) { o.onReady = fn }
}

// Subscribe registers a 1-buffered channel under `name` and returns
// it. If `name` already exists the previous channel is closed first
// (resubscribe semantics, see TestBus_ResubscribeClosesOld). Pass
// WithReady to get a hook that fires after registration is complete
// but before Subscribe returns.
func (b *Bus) Subscribe(name string, opts ...SubscribeOption) <-chan Snapshot {
	cfg := subscribeOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		ch := make(chan Snapshot)
		close(ch)
		if cfg.onReady != nil {
			cfg.onReady()
		}
		return ch
	}
	if existing, ok := b.subs[name]; ok {
		close(existing)
	}
	ch := make(chan Snapshot, 1)
	b.subs[name] = ch
	b.mu.Unlock()

	if cfg.onReady != nil {
		cfg.onReady()
	}
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
				// Two publishers raced: both hit the default case,
				// both drained, and one's re-send now finds the slot
				// taken by the other. The latest snapshot is still in
				// the channel, so latest-wins semantics hold — this
				// log is informational, not an error.
				b.logger.LogAttrs(ctx, slog.LevelDebug,
					"publish lost to concurrent publish (current value preserved, latest-wins semantics)",
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
