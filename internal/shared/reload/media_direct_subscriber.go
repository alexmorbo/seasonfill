package reload

import (
	"context"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// MediaDirectSubscriber applies the app_config media_direct flag to the
// media handler on every runtime-config publish. The handler reads the
// flag per-request via an atomic.Bool, so a PUT /config/runtime that flips
// media_direct takes effect on the next media request without a pod
// restart. `set` is the handler's SetMediaDirect method, injected as a
// plain func so this package keeps no import on the mediaproxy REST layer.
// set is nil-OK (minimal/test wirings) — apply then no-ops.
type MediaDirectSubscriber struct {
	set    func(bool)
	logger *slog.Logger
}

func NewMediaDirectSubscriber(set func(bool), logger *slog.Logger) *MediaDirectSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &MediaDirectSubscriber{set: set, logger: logger}
}

func (s *MediaDirectSubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	runLoop(ctx, bus, "mediaDirect", s.logger, s.apply, ready)
}

func (s *MediaDirectSubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	if s.set != nil {
		s.set(snap.MediaDirect)
	}
	return nil
}
