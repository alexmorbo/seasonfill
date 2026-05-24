package reload

import (
	"context"
	"log/slog"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// ClientLister returns the current live sonarr client list. In
// production this is `SonarrClientsSubscriber.View().All()`. The
// indirection keeps healthRegistry and sonarrClients subscribers
// decoupled — health doesn't care HOW the clients are managed,
// only what the current set is.
type ClientLister func() []ports.SonarrClient

// HealthChecker is the (small) subset of healthcheck.Checker the
// reload subscriber needs.
type HealthChecker interface {
	ReplaceClients([]ports.SonarrClient)
}

// HealthRegistrySubscriber re-seeds the health registry whenever
// instances are added or removed.
type HealthRegistrySubscriber struct {
	checker HealthChecker
	lister  ClientLister
	logger  *slog.Logger
}

func NewHealthRegistrySubscriber(checker HealthChecker, lister ClientLister, logger *slog.Logger) *HealthRegistrySubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthRegistrySubscriber{checker: checker, lister: lister, logger: logger}
}

// Run blocks until ctx is done.
func (s *HealthRegistrySubscriber) Run(ctx context.Context, bus *runtime.Bus) {
	runLoop(ctx, bus, "healthRegistry", s.logger, s.apply)
}

// apply re-reads the live client list (which the sonarrClients
// subscriber has already updated by virtue of being subscribed to
// the same bus) and replays it into the checker.
//
// Ordering note: the bus delivers concurrently to every subscriber.
// healthRegistry might run before sonarrClients on a given snapshot;
// when that happens we publish stale data this tick and the NEXT
// tick (or the eventual re-publish from cmd/server startup) fixes
// it. Acceptable per the fail-open stub decision.
func (s *HealthRegistrySubscriber) apply(_ context.Context, _ runtime.Snapshot) error {
	s.checker.ReplaceClients(s.lister())
	return nil
}
