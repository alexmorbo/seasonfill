package reload

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// DrainDelay is how long a removed sonarr client lingers in
// pendingRemoval before being dropped. Long enough for in-flight
// scan/grab calls to finish (locked at 30s; can be reduced in
// tests via WithDrainDelay).
const DrainDelay = 30 * time.Second

// SonarrClientFactory builds a fresh per-instance sonarr client.
// Production wiring captures the global limiter + observer closures
// at construction so they don't have to be plumbed through the
// snapshot.
type SonarrClientFactory func(snap runtime.InstanceSnapshot) ports.SonarrClient

// ClientsView is a read-only snapshot of the live client map; the
// instances handler / scan UC consume one of these per request.
type ClientsView struct {
	byName map[string]ports.SonarrClient
}

// ByName returns the client for `name`, or nil + false if absent.
func (v *ClientsView) ByName(name string) (ports.SonarrClient, bool) {
	c, ok := v.byName[name]
	return c, ok
}

// All returns every live client. Order is unspecified.
func (v *ClientsView) All() []ports.SonarrClient {
	out := make([]ports.SonarrClient, 0, len(v.byName))
	for _, c := range v.byName {
		out = append(out, c)
	}
	return out
}

type pendingEntry struct {
	client ports.SonarrClient
	config runtime.InstanceSnapshot
	timer  *time.Timer
}

// SonarrClientsSubscriber owns the live `map[string]ports.SonarrClient`
// and rebuilds it on every snapshot. Removed instances are kept in
// pendingRemoval for DrainDelay so in-flight calls finish; new
// instances with the same name during the drain window reuse the
// pending client.
type SonarrClientsSubscriber struct {
	mu             sync.RWMutex
	live           map[string]ports.SonarrClient
	configs        map[string]runtime.InstanceSnapshot
	pendingRemoval map[string]pendingEntry
	factory        SonarrClientFactory
	logger         *slog.Logger
	drainDelay     time.Duration
}

func NewSonarrClientsSubscriber(boot map[string]ports.SonarrClient, bootConfigs map[string]runtime.InstanceSnapshot, factory SonarrClientFactory, logger *slog.Logger) *SonarrClientsSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	live := make(map[string]ports.SonarrClient, len(boot))
	for k, v := range boot {
		live[k] = v
	}
	cfgs := make(map[string]runtime.InstanceSnapshot, len(bootConfigs))
	for k, v := range bootConfigs {
		cfgs[k] = v
	}
	return &SonarrClientsSubscriber{
		live: live, configs: cfgs,
		pendingRemoval: map[string]pendingEntry{},
		factory: factory, logger: logger,
		drainDelay: DrainDelay,
	}
}

// WithDrainDelay overrides the 30s drain delay. Test-only.
func (s *SonarrClientsSubscriber) WithDrainDelay(d time.Duration) *SonarrClientsSubscriber {
	s.drainDelay = d
	return s
}

// View returns a stable read-only snapshot of the live client map.
// Callers must not retain the map across reload boundaries; take a
// fresh View() per request setup instead.
func (s *SonarrClientsSubscriber) View() *ClientsView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]ports.SonarrClient, len(s.live))
	for k, v := range s.live {
		cp[k] = v
	}
	return &ClientsView{byName: cp}
}

// Run blocks until ctx is done. Cancels every pending-removal timer
// on exit so the test process doesn't leak goroutines.
func (s *SonarrClientsSubscriber) Run(ctx context.Context, bus *runtime.Bus) {
	defer s.cancelAllPending()
	runLoop(ctx, bus, "sonarrClients", s.logger, s.apply)
}

func (s *SonarrClientsSubscriber) cancelAllPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, p := range s.pendingRemoval {
		p.timer.Stop()
		delete(s.pendingRemoval, name)
	}
}

func (s *SonarrClientsSubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	wantByName := make(map[string]runtime.InstanceSnapshot, len(snap.Instances))
	for _, inst := range snap.Instances {
		wantByName[inst.Name] = inst
	}

	// 1. For each desired instance: reuse, resurrect-from-pending,
	//    or build new.
	nextLive := make(map[string]ports.SonarrClient, len(wantByName))
	nextCfgs := make(map[string]runtime.InstanceSnapshot, len(wantByName))
	for name, want := range wantByName {
		if existing, ok := s.live[name]; ok && sameClientConfig(s.configs[name], want) {
			nextLive[name] = existing
			nextCfgs[name] = want
			continue
		}
		if pending, ok := s.pendingRemoval[name]; ok {
			// Re-added during drain window AND its config matches
			// the still-live pending client → reuse it.
			if sameClientConfig(pending.config, want) {
				pending.timer.Stop()
				delete(s.pendingRemoval, name)
				nextLive[name] = pending.client
				nextCfgs[name] = want
				continue
			}
			// Config changed mid-drain — drop the pending entry
			// immediately; we're about to build a fresh one.
			pending.timer.Stop()
			delete(s.pendingRemoval, name)
		}
		client := s.factory(want)
		if client == nil {
			return fmt.Errorf("sonarr factory returned nil for instance %q", name)
		}
		nextLive[name] = client
		nextCfgs[name] = want
	}

	// 2. Schedule drain for every instance that disappeared from
	//    `live` but isn't already in pendingRemoval.
	for name, client := range s.live {
		if _, kept := nextLive[name]; kept {
			continue
		}
		if _, already := s.pendingRemoval[name]; already {
			continue
		}
		victim := name
		cfg := s.configs[name]
		t := time.AfterFunc(s.drainDelay, func() {
			s.mu.Lock()
			delete(s.pendingRemoval, victim)
			s.mu.Unlock()
			s.logger.Info("reload.sonarrClients.drained",
				slog.String("instance", victim))
		})
		s.pendingRemoval[name] = pendingEntry{client: client, config: cfg, timer: t}
	}

	s.live = nextLive
	s.configs = nextCfgs
	return nil
}

// sameClientConfig answers "can we keep the existing client?" — only
// fields that the sonarr client captures at construction (URL,
// APIKey, Timeout, SearchTimeout, RateLimit) need to match. Tags /
// search rules are read by scan UC, not the client.
func sameClientConfig(a, b runtime.InstanceSnapshot) bool {
	return a.URL == b.URL &&
		a.APIKey == b.APIKey &&
		a.Timeout == b.Timeout &&
		a.SearchTimeout == b.SearchTimeout &&
		a.RateLimit.RPM == b.RateLimit.RPM &&
		a.RateLimit.Burst == b.RateLimit.Burst
}
