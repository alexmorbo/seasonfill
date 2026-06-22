package loops

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
)

// DefaultWatchdogStateInterval is the cadence for the watchdog state
// collector. 5 minutes balances dashboard freshness vs DB load: the
// counts are slowly-changing and survive missed scrapes.
const DefaultWatchdogStateInterval = 5 * time.Minute

// WatchdogBlacklistCounter is the narrow port the collector calls.
// *watchdog/persistence.WatchdogBlacklistRepository satisfies it via
// CountByInstance.
type WatchdogBlacklistCounter interface {
	CountByInstance(ctx context.Context, instance domain.InstanceName) (int, error)
}

// WatchdogCooldownCounter is the narrow port for the cooldown counts.
// *watchdog/persistence.CooldownRepository satisfies it via
// CountActiveByScopeGroupedByInstance.
type WatchdogCooldownCounter interface {
	CountActiveByScopeGroupedByInstance(
		ctx context.Context, scope cooldown.Scope, now time.Time,
	) (map[domain.InstanceName]int, error)
}

// WatchdogStateInstances is the registry surface. Same shape as the
// capacity loop's QbitCapacityInstances.
type WatchdogStateInstances interface {
	List() []domain.InstanceName
}

// WatchdogStateInstancesFunc is the func adapter — production wires a
// closure over the Sonarr instance holder snapshot through this type.
type WatchdogStateInstancesFunc func() []domain.InstanceName

// List implements WatchdogStateInstances.
func (f WatchdogStateInstancesFunc) List() []domain.InstanceName { return f() }

// WatchdogStateMetrics is the narrow metric port. Production:
// observability.WatchdogMetricsAdapter.
type WatchdogStateMetrics interface {
	SetCooldownPending(instance domain.InstanceName, count int)
	SetBlacklistSize(instance domain.InstanceName, size int)
}

// WatchdogStateCollector is the 5min state-gauge collector. Wires the
// cooldown_pending + blacklist_size gauges; the regrab_candidates
// gauge is published per-cycle by cmd/server/loops/regrab.go, not
// here. Story 479b.
type WatchdogStateCollector struct {
	bl      WatchdogBlacklistCounter
	cd      WatchdogCooldownCounter
	insts   WatchdogStateInstances
	metrics WatchdogStateMetrics
	bgWG    *sync.WaitGroup
	logger  *slog.Logger

	intervalNS atomic.Int64
}

// NewWatchdogStateCollector wires the collector. interval <= 0 →
// DefaultWatchdogStateInterval. log nil → DomainLogger("watchdog").
func NewWatchdogStateCollector(
	bl WatchdogBlacklistCounter,
	cd WatchdogCooldownCounter,
	insts WatchdogStateInstances,
	metricsPort WatchdogStateMetrics,
	interval time.Duration,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) *WatchdogStateCollector {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "watchdog")
	}
	if interval <= 0 {
		interval = DefaultWatchdogStateInterval
	}
	c := &WatchdogStateCollector{
		bl: bl, cd: cd, insts: insts, metrics: metricsPort,
		bgWG: bgWG, logger: log,
	}
	c.intervalNS.Store(int64(interval))
	return c
}

// Run is the main loop. Immediate first tick so a fresh pod has
// gauges populated without waiting one full interval. bgWG drain on
// ctx cancel.
func (c *WatchdogStateCollector) Run(ctx context.Context) {
	if c.bgWG != nil {
		defer c.bgWG.Done()
	}
	c.tick(ctx)
	timer := time.NewTimer(time.Duration(c.intervalNS.Load()))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			c.tick(ctx)
			timer.Reset(time.Duration(c.intervalNS.Load()))
		}
	}
}

// tick performs one collection pass. A per-instance error in the
// blacklist counter is logged WARN but does not abort the iteration —
// one bad instance must not stall the rest. A cooldown count error
// degrades the cooldown_pending publish to "skip per-instance update"
// (the gauge retains its last-good value) — the loop keeps publishing
// blacklist sizes.
func (c *WatchdogStateCollector) tick(ctx context.Context) {
	if c.insts == nil || c.bl == nil || c.cd == nil || c.metrics == nil {
		return
	}
	now := time.Now().UTC()

	cdCounts, err := c.cd.CountActiveByScopeGroupedByInstance(ctx, cooldown.ScopeRegrabRetry, now)
	if err != nil {
		c.logger.WarnContext(ctx, "watchdog_state_cooldown_count_failed",
			slog.String("error", err.Error()))
	}

	for _, inst := range c.insts.List() {
		// Publish cooldown pending regardless of err: nil map →
		// zero count, which is the correct "cleared after sweep"
		// reading. When the count succeeded but this instance has
		// no entries, the map lookup returns 0 by default — same
		// effect.
		if err == nil {
			c.metrics.SetCooldownPending(inst, cdCounts[inst])
		}

		count, err := c.bl.CountByInstance(ctx, inst)
		if err != nil {
			c.logger.WarnContext(ctx, "watchdog_state_blacklist_count_failed",
				slog.String("instance_name", string(inst)),
				slog.String("error", err.Error()))
			continue
		}
		c.metrics.SetBlacklistSize(inst, count)
	}
}
