package reload

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

const DrainDelay = 30 * time.Second

type SonarrClientFactory func(snap runtime.InstanceSnapshot) ports.SonarrClient

// OnAppliedFunc is invoked synchronously at the tail of every successful
// apply, while the subscriber lock is still held. Callees MUST NOT call
// back into the subscriber and MUST NOT perform I/O — the only intended
// implementations are atomic stores (scanUC.SwapInstances) and pure-CPU
// map copies (holder.replace). The map handed in is freshly allocated
// for this call and the callee owns it.
type OnAppliedFunc func(snap runtime.Snapshot, clients map[string]ports.SonarrClient)

type ClientsView struct {
	byName map[string]ports.SonarrClient
}

func (v *ClientsView) ByName(name string) (ports.SonarrClient, bool) {
	c, ok := v.byName[name]
	return c, ok
}

func (v *ClientsView) All() []ports.SonarrClient {
	out := make([]ports.SonarrClient, 0, len(v.byName))
	for _, c := range v.byName {
		out = append(out, c)
	}
	return out
}

// pendingEntry is one in-flight drain. `deadline` is wall-time; the sweeper
// drops the entry once `time.Now().After(deadline)`. `config` is carried
// only to detect a same-config re-add inside the drain window so we can
// hand the still-warm client back without a factory call.
type pendingEntry struct {
	name     string
	client   ports.SonarrClient
	config   runtime.InstanceSnapshot
	deadline time.Time
}

type SonarrClientsSubscriber struct {
	mu             sync.RWMutex
	live           map[string]ports.SonarrClient
	configs        map[string]runtime.InstanceSnapshot
	pendingRemoval map[string]pendingEntry
	factory        SonarrClientFactory
	onApplied      OnAppliedFunc
	logger         *slog.Logger
	drainDelay     time.Duration
	bgWG           *sync.WaitGroup
}

func NewSonarrClientsSubscriber(boot map[string]ports.SonarrClient, factory SonarrClientFactory, logger *slog.Logger) *SonarrClientsSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	live := make(map[string]ports.SonarrClient, len(boot))
	for k, v := range boot {
		live[k] = v
	}
	return &SonarrClientsSubscriber{
		live:           live,
		configs:        map[string]runtime.InstanceSnapshot{},
		pendingRemoval: map[string]pendingEntry{},
		factory:        factory,
		logger:         logger,
		drainDelay:     DrainDelay,
	}
}

// WithDrainDelay overrides the 30s drain delay. MUST be called before Run;
// mutating drainDelay after Run starts is a data race. Test-only.
func (s *SonarrClientsSubscriber) WithDrainDelay(d time.Duration) *SonarrClientsSubscriber {
	s.drainDelay = d
	return s
}

// WithWaitGroup registers the sweeper goroutine on a caller-owned WG so
// shutdown can wait for the drain loop to finish. cmd/server passes the
// shared bgWG. Tests may omit; Run falls back to a private WG.
func (s *SonarrClientsSubscriber) WithWaitGroup(wg *sync.WaitGroup) *SonarrClientsSubscriber {
	s.bgWG = wg
	return s
}

// WithOnApplied wires a synchronous post-apply hook. The hook is invoked
// while the subscriber lock is held; see OnAppliedFunc for the contract.
// MUST be called before Run.
func (s *SonarrClientsSubscriber) WithOnApplied(fn OnAppliedFunc) *SonarrClientsSubscriber {
	s.onApplied = fn
	return s
}

func (s *SonarrClientsSubscriber) View() *ClientsView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]ports.SonarrClient, len(s.live))
	for k, v := range s.live {
		cp[k] = v
	}
	return &ClientsView{byName: cp}
}

// Run blocks until ctx is done. Starts the sweeper goroutine (tracked on
// bgWG when supplied) before entering runLoop; sweeper exits and flushes
// remaining pending entries on ctx.Done().
func (s *SonarrClientsSubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	wg := s.bgWG
	if wg == nil {
		wg = &sync.WaitGroup{}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.sweepDrains(ctx)
	}()
	runLoop(ctx, bus, "sonarrClients", s.logger, s.apply, ready)
	if s.bgWG == nil {
		wg.Wait()
	}
}

// sweepInterval is `min(drainDelay/2, 1s)` clamped at 50ms — fast enough to
// observe drain in 100ms-delay tests, slow enough to be cheap in prod.
func (s *SonarrClientsSubscriber) sweepInterval() time.Duration {
	half := s.drainDelay / 2
	const lo, hi = 50 * time.Millisecond, time.Second
	if half < lo {
		return lo
	}
	if half > hi {
		return hi
	}
	return half
}

// sweepDrains ticks at sweepInterval() and drops every pendingRemoval entry
// whose deadline has passed. On ctx.Done it flushes every remaining entry
// (logs `drained` for each) so shutdown is observable in tests.
func (s *SonarrClientsSubscriber) sweepDrains(ctx context.Context) {
	t := time.NewTicker(s.sweepInterval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flushAllPending()
			return
		case <-t.C:
			s.sweepExpired(time.Now())
		}
	}
}

func (s *SonarrClientsSubscriber) sweepExpired(now time.Time) {
	s.mu.Lock()
	expired := make([]string, 0, len(s.pendingRemoval))
	for name, p := range s.pendingRemoval {
		if !now.Before(p.deadline) {
			expired = append(expired, name)
		}
	}
	sort.Strings(expired)
	for _, name := range expired {
		delete(s.pendingRemoval, name)
	}
	s.mu.Unlock()
	for _, name := range expired {
		s.logger.Info("reload.sonarrClients.drained",
			slog.String("instance", name))
	}
}

// flushAllPending is the shutdown path — drops every pending entry
// regardless of deadline so `bgWG.Wait` doesn't block on a 30s timer.
func (s *SonarrClientsSubscriber) flushAllPending() {
	s.mu.Lock()
	names := make([]string, 0, len(s.pendingRemoval))
	for name := range s.pendingRemoval {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		delete(s.pendingRemoval, name)
	}
	s.mu.Unlock()
	for _, name := range names {
		s.logger.Info("reload.sonarrClients.drained",
			slog.String("instance", name),
			slog.String("reason", "shutdown"))
	}
}

// apply rebuilds clients from the snapshot and invokes onApplied while
// holding s.mu to serialize holder updates with client changes.
func (s *SonarrClientsSubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	wantByName := make(map[string]runtime.InstanceSnapshot, len(snap.Instances))
	for _, inst := range snap.Instances {
		wantByName[inst.Name] = inst
	}

	nextLive := make(map[string]ports.SonarrClient, len(wantByName))
	nextCfgs := make(map[string]runtime.InstanceSnapshot, len(wantByName))
	for name, want := range wantByName {
		if pending, ok := s.pendingRemoval[name]; ok && sameClientConfig(pending.config, want) {
			delete(s.pendingRemoval, name)
			nextLive[name] = pending.client
			nextCfgs[name] = want
			continue
		}
		if _, ok := s.pendingRemoval[name]; ok {
			delete(s.pendingRemoval, name)
		}
		client := s.factory(want)
		if client == nil {
			return fmt.Errorf("sonarr factory returned nil for instance %q", name)
		}
		nextLive[name] = client
		nextCfgs[name] = want
	}

	now := time.Now()
	for name, client := range s.live {
		if _, kept := nextLive[name]; kept {
			continue
		}
		if _, already := s.pendingRemoval[name]; already {
			continue
		}
		cfg := s.configs[name] // carry forward the last known config for re-add matching
		s.pendingRemoval[name] = pendingEntry{
			name:     name,
			client:   client,
			config:   cfg,
			deadline: now.Add(s.drainDelay),
		}
	}

	s.live = nextLive
	s.configs = nextCfgs

	if s.onApplied != nil {
		// Allocate a defensive copy so the callee can retain the map
		// without aliasing into s.live. Cheap: pointer-sized entries.
		cp := make(map[string]ports.SonarrClient, len(nextLive))
		for k, v := range nextLive {
			cp[k] = v
		}
		s.onApplied(snap, cp)
	}
	return nil
}

func sameClientConfig(a, b runtime.InstanceSnapshot) bool {
	return a.URL == b.URL &&
		a.APIKey == b.APIKey &&
		a.Timeout == b.Timeout &&
		a.SearchTimeout == b.SearchTimeout &&
		a.RateLimit.RPM == b.RateLimit.RPM &&
		a.RateLimit.Burst == b.RateLimit.Burst
}
