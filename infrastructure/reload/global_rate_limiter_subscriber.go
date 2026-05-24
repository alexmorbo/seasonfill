package reload

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// GlobalLimiterFactory builds a fresh global limiter from a
// {rpm, burst} pair. Production wiring captures the
// `observability.IncRateLimitThrottled("", "global")` observer.
type GlobalLimiterFactory func(rpm, burst int) *ratelimit.Limiter

// DefaultGlobalLimiterFactory matches cmd/server's boot-time wiring.
func DefaultGlobalLimiterFactory(rpm, burst int) *ratelimit.Limiter {
	return ratelimit.NewFromRPMWithOptions(rpm, burst,
		ratelimit.WithObserver("global", func(scope string) {
			observability.IncRateLimitThrottled("", scope)
		}))
}

// LimiterPointer is the atomic.Pointer used by sonarr clients to
// read the current global limiter on every call. Pass it via a
// closure to sonarr.NewWithOptions: `WithGlobalLimiter(lp.Load())`
// won't reload — the client must capture the pointer and dereference
// per-call. cmd/server wires a `func() *ratelimit.Limiter { return lp.Load() }`.
type LimiterPointer = atomic.Pointer[ratelimit.Limiter]

// GlobalRateLimiterSubscriber owns the atomic and rebuilds the
// limiter whenever {RPM, Burst} differ from the last applied pair.
type GlobalRateLimiterSubscriber struct {
	ptr     *LimiterPointer
	factory GlobalLimiterFactory
	logger  *slog.Logger

	mu atomic.Pointer[runtime.RateLimitSnapshot]
}

func NewGlobalRateLimiterSubscriber(ptr *LimiterPointer, factory GlobalLimiterFactory, logger *slog.Logger) *GlobalRateLimiterSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalRateLimiterSubscriber{ptr: ptr, factory: factory, logger: logger}
}

func (s *GlobalRateLimiterSubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	runLoop(ctx, bus, "globalRateLimiter", s.logger, s.apply, ready)
}

func (s *GlobalRateLimiterSubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	want := snap.GlobalRateLimit
	if prev := s.mu.Load(); prev != nil && *prev == want {
		return nil
	}
	next := s.factory(want.RPM, want.Burst)
	s.ptr.Store(next)
	cp := want
	s.mu.Store(&cp)
	return nil
}
