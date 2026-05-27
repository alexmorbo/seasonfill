package watchdog

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// Rechecker is the (small) surface the watchdog needs from the healthcheck.
type Rechecker interface {
	RecheckByName(ctx context.Context, name string)
}

// Registry is the read-only surface the watchdog needs.
type Registry interface {
	Snapshot() []instance.Snapshot
}

type Watchdog struct {
	reg       Registry
	checker   Rechecker
	logger    *slog.Logger
	mu        sync.RWMutex
	instances map[string]config.HealthCheckConfig
}

// New constructs a Watchdog. cfgByName maps instance name to its
// HealthCheckConfig (already defaulted by config.ApplyInstanceDefaults).
func New(reg Registry, checker Rechecker, logger *slog.Logger, cfgByName map[string]config.HealthCheckConfig) *Watchdog {
	return &Watchdog{reg: reg, checker: checker, logger: logger, instances: cfgByName}
}

// SwapConfigs atomically replaces the per-instance HealthCheckConfig
// map. Called from buildOnAppliedFanout on every reload publish so
// new/edited/deleted instances reflect in the recheck schedule.
func (w *Watchdog) SwapConfigs(next map[string]config.HealthCheckConfig) {
	w.mu.Lock()
	w.instances = next
	w.mu.Unlock()
}

// Run blocks until ctx is done. The watchdog ticks at the shortest configured
// interval and rechecks each Unavailable* instance individually based on its
// own state-specific cadence.
func (w *Watchdog) Run(ctx context.Context) {
	tick := w.shortest()
	if tick <= 0 {
		tick = time.Minute
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	w.mu.RLock()
	last := make(map[string]time.Time, len(w.instances))
	w.mu.RUnlock()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			seen := make(map[string]struct{}, len(last))
			for _, snap := range w.reg.Snapshot() {
				seen[snap.Name] = struct{}{}
				if snap.Health == instance.HealthAvailable {
					continue
				}
				w.mu.RLock()
				cfg, ok := w.instances[snap.Name]
				w.mu.RUnlock()
				if !ok {
					continue
				}
				due := w.intervalFor(snap.Health, cfg)
				if !last[snap.Name].IsZero() && now.Sub(last[snap.Name]) < due {
					continue
				}
				last[snap.Name] = now
				w.logger.DebugContext(ctx, "watchdog_recheck",
					slog.String("instance", snap.Name),
					slog.String("state", string(snap.Health)),
				)
				w.checker.RecheckByName(ctx, snap.Name)
			}
			// Deferred-item #3: drop names no longer in the registry snapshot
			// so a future config-reload removing an instance does not leak
			// the entry forever.
			for name := range last {
				if _, ok := seen[name]; !ok {
					delete(last, name)
				}
			}
		}
	}
}

func (w *Watchdog) intervalFor(h instance.Health, cfg config.HealthCheckConfig) time.Duration {
	switch h {
	case instance.HealthUnavailableAuth:
		return cfg.RecheckIntervalAuth
	default:
		return cfg.RecheckIntervalNetwork
	}
}

func (w *Watchdog) shortest() time.Duration {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var minD time.Duration
	for _, cfg := range w.instances {
		for _, d := range []time.Duration{cfg.RecheckIntervalNetwork, cfg.RecheckIntervalAuth} {
			if d <= 0 {
				continue
			}
			if minD == 0 || d < minD {
				minD = d
			}
		}
	}
	return minD
}
