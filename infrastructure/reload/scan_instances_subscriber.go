package reload

import (
	"context"
	"log/slog"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// ClientForName returns the live sonarr client for `name`. In
// production this resolves through SonarrClientsSubscriber.View().
// The indirection means scanInstances doesn't need a direct
// reference to the sonarrClients subscriber — it only needs a way
// to look up the current client per snapshot instance.
type ClientForName func(name string) (ports.SonarrClient, bool)

// ScanInstancesSubscriber rebuilds the scan UC's instances slice
// (and by-name map) on every snapshot. Calls `scanUC.SwapInstances`
// + `byNameMap.Replace` (handled via a SwapByName setter on the
// usecase if present; here we keep the indirection minimal and
// expose a swap callback so cmd/server can wire whatever map it
// owns).
type ScanInstancesSubscriber struct {
	scanUC    *scan.UseCase
	clientFor ClientForName
	onByName  func(map[string]scan.Instance)
	logger    *slog.Logger
}

// NewScanInstancesSubscriber. `onByName` is a callback cmd/server
// uses to update its `scanInstancesByName` map (consumed by the
// rescan UC). It MAY be nil if the caller doesn't share that map.
func NewScanInstancesSubscriber(scanUC *scan.UseCase, clientFor ClientForName, onByName func(map[string]scan.Instance), logger *slog.Logger) *ScanInstancesSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScanInstancesSubscriber{
		scanUC: scanUC, clientFor: clientFor,
		onByName: onByName, logger: logger,
	}
}

func (s *ScanInstancesSubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	runLoop(ctx, bus, "scanInstances", s.logger, s.apply, ready)
}

func (s *ScanInstancesSubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	nextSlice := make([]scan.Instance, 0, len(snap.Instances))
	nextMap := make(map[string]scan.Instance, len(snap.Instances))
	for _, inst := range snap.Instances {
		client, ok := s.clientFor(inst.Name)
		if !ok {
			// Sonarr-clients subscriber hasn't caught up yet; skip
			// this instance for the current tick. The next publish
			// (or cmd/server's startup re-publish) fixes it.
			s.logger.Warn("scanInstances: client not yet available for instance",
				slog.String("instance", inst.Name))
			continue
		}
		cfg := config.SonarrInstance{
			Name: inst.Name, URL: inst.URL, APIKey: inst.APIKey,
			Mode: inst.Mode, Timeout: inst.Timeout, SearchTimeout: inst.SearchTimeout,
		}
		si := scan.Instance{Config: cfg, Client: client}
		nextSlice = append(nextSlice, si)
		nextMap[inst.Name] = si
	}
	s.scanUC.SwapInstances(nextSlice)
	if s.onByName != nil {
		s.onByName(nextMap)
	}
	return nil
}
