package reload

import (
	"context"
	"log/slog"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// HealthChecker is the (small) subset of healthcheck.Checker the
// reload subscriber needs. The two-arg signature mirrors story 028c:
// `clients` drives the polling loop; `names` drives the registry
// membership diff. They MUST refer to the same instance set.
type HealthChecker interface {
	ReplaceClients(clients []ports.SonarrClient, names []string)
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
func (s *HealthRegistrySubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	runLoop(ctx, bus, "healthRegistry", s.logger, s.apply, ready)
}

// apply re-reads the live client list (which the sonarrClients
// subscriber has already updated by virtue of being subscribed to
// the same bus) and replays it into the checker. The name list is
// derived from the same live clients to guarantee names and clients
// stay aligned even if the snapshot delivery order races with
// sonarrClients.
//
// Ordering note: the bus delivers concurrently to every subscriber.
// healthRegistry might run before sonarrClients on a given snapshot;
// when that happens we publish stale data this tick and the NEXT
// tick (or the eventual re-publish from cmd/server startup) fixes
// it. Acceptable per the fail-open stub decision.
func (s *HealthRegistrySubscriber) apply(_ context.Context, _ runtime.Snapshot) error {
	clients := s.lister()
	names := make([]string, 0, len(clients))
	for _, c := range clients {
		names = append(names, c.Name())
	}
	s.checker.ReplaceClients(clients, names)
	return nil
}
