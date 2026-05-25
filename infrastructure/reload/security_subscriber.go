package reload

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// SecuritySubscriber watches snap.Security.AllowPrivateTargets and flips
// a *atomic.Bool that gates netguard.Guard inside the probe http.Client.
// Client captures the bool via closure passed at construction in
// cmd/server/main.go, so a snapshot change reaches the dialer on the next
// probe without recycling the client. Shape mirrors AuthMiddlewareSubscriber:
// 1-buffered ready-once loop via runLoop, fail-open on apply errors (none
// today — pure atomic store), only-log-on-change.
type SecuritySubscriber struct {
	allowPrivate *atomic.Bool
	logger       *slog.Logger
}

func NewSecuritySubscriber(allowPrivate *atomic.Bool, logger *slog.Logger) *SecuritySubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &SecuritySubscriber{allowPrivate: allowPrivate, logger: logger}
}

func (s *SecuritySubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	runLoop(ctx, bus, "security", s.logger, s.apply, ready)
}

func (s *SecuritySubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	want := snap.Security.AllowPrivateTargets
	prev := s.allowPrivate.Load()
	if prev == want {
		return nil
	}
	s.allowPrivate.Store(want)
	s.logger.Info("security.allow_private_targets.changed",
		slog.Bool("prev", prev),
		slog.Bool("next", want))
	return nil
}
