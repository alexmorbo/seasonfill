package healthcheck

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/instance"
	"github.com/alexmorbo/seasonfill/internal/observability"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// preflightConcurrency bounds the number of in-flight checkOne probes
// during a single Preflight pass. Sequential iteration over M instances
// at 10s per timeout meant worst-case wall time of M*10s; with the
// limit, worst-case is ceil(M/N)*10s. 4 is a deliberate trade-off:
// large enough to mask the typical 1-3 instance setup, small enough to
// avoid hammering shared Sonarr backends during a recovery storm.
const preflightConcurrency = 4

// Checker periodically polls every Sonarr instance and records its
// health into a shared *instance.Registry. The registry pointer is
// constructed ONCE in New and never reassigned afterwards — watchdog
// and the scan UC keep their boot-time reference forever. Membership
// changes go through Registry.SetNames; the client list goes through
// an atomic.Pointer swap. See story 028c.
type Checker struct {
	db        *gorm.DB
	instances atomic.Pointer[[]ports.SonarrClient]
	registry  *instance.Registry
	// preflightRunning is a single-flight gate: concurrent Preflight()
	// calls coalesce to one in-flight run. Burst CRUD (e.g. several
	// rapid reload publishes during a config sync) would otherwise
	// spawn overlapping goroutines that double-fire metrics + listener
	// and race on LastCheckAt ordering. See story 032h.
	preflightRunning atomic.Bool
}

func New(db *gorm.DB, instances []ports.SonarrClient) *Checker {
	names := make([]string, 0, len(instances))
	for _, inst := range instances {
		names = append(names, inst.Name())
	}
	reg := instance.NewRegistry(names).WithListener(metricsListener{})
	c := &Checker{db: db, registry: reg}
	// atomic.Pointer must never load nil — seed with the boot slice
	// (or an empty slice if instances is nil).
	cp := append([]ports.SonarrClient(nil), instances...)
	c.instances.Store(&cp)
	return c
}

// Registry returns the underlying health registry. The watchdog and the
// `/api/v1/instances` handler (004d) share it. The returned pointer is
// stable for the life of the Checker — reload does NOT swap it.
func (c *Checker) Registry() *instance.Registry { return c.registry }

// Preflight probes every known instance and updates the registry.
// Concurrent calls are coalesced via a single-flight gate: if another
// preflight is mid-flight, this call returns immediately. Within a
// single pass, instances are probed in parallel with a bounded worker
// pool (preflightConcurrency) so the total wall time is
// ceil(M/N)*timeout instead of M*timeout. Per-instance semantics
// (ReplaceClients/MarkAvailable/MarkUnavailable) are unchanged — only
// the iteration strategy differs.
func (c *Checker) Preflight(ctx context.Context) {
	if !c.preflightRunning.CompareAndSwap(false, true) {
		return
	}
	defer c.preflightRunning.Store(false)
	clients := *c.instances.Load()
	if len(clients) == 0 {
		return
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(preflightConcurrency)
	for _, inst := range clients {
		client := inst
		g.Go(func() error {
			c.checkOne(gctx, client)
			return nil
		})
	}
	_ = g.Wait()
}

func (c *Checker) checkOne(ctx context.Context, client ports.SonarrClient) {
	name := client.Name()
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err := client.SystemStatus(callCtx)
	cancel()
	now := time.Now().UTC()
	if err == nil {
		c.registry.MarkAvailable(name, now)
		observability.SetInstanceAvailable(shareddomain.InstanceName(name), true)
		return
	}
	state := instance.HealthUnavailableUnknown
	switch {
	case errors.Is(err, domain.ErrInstanceUnauthorized):
		state = instance.HealthUnavailableAuth
	case errors.Is(err, domain.ErrInstanceNetwork):
		state = instance.HealthUnavailableNetwork
	case errors.Is(err, domain.ErrInstanceSelfThrottled):
		state = instance.HealthSelfThrottled
	}
	c.registry.MarkUnavailable(name, state, err.Error(), now)
	// SelfThrottled is still "we couldn't reach Sonarr this round" from
	// the gauge perspective but the typed health code (see healthCode)
	// gives dashboards the nuance.
	observability.SetInstanceAvailable(shareddomain.InstanceName(name), false)
}

// RecheckByName runs preflight against a single instance — used by the
// watchdog when it wants to test recovery for one instance only.
func (c *Checker) RecheckByName(ctx context.Context, name string) {
	clients := *c.instances.Load()
	for _, inst := range clients {
		if inst.Name() == name {
			c.checkOne(ctx, inst)
			return
		}
	}
}

// Run starts the periodic preflight loop. It executes one preflight pass
// immediately so the registry is populated before the ticker fires.
func (c *Checker) Run(ctx context.Context, period time.Duration) {
	c.Preflight(ctx)
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Preflight(ctx)
		}
	}
}

func (c *Checker) AnyInstanceAvailable() bool { return c.registry.AnyAvailable() }

func (c *Checker) DatabaseUp(ctx context.Context) bool {
	sqlDB, err := c.db.DB()
	if err != nil {
		return false
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return sqlDB.PingContext(pctx) == nil
}

// Snapshot returns the current health entry for every known instance.
func (c *Checker) Snapshot() []instance.Snapshot { return c.registry.Snapshot() }

// metricsListener drives the typed health gauges + transitions counter.
// The legacy `seasonfill_instances_available` gauge is kept up-to-date
// alongside the new `seasonfill_instance_health` so existing dashboards
// continue to work during the migration.
type metricsListener struct{}

func (metricsListener) OnCheck(name string, h instance.Health, at time.Time) {
	inst := shareddomain.InstanceName(name)
	observability.SetInstanceAvailable(inst, h == instance.HealthAvailable)
	observability.SetInstanceHealth(inst, healthCode(h))
	observability.SetInstanceLastCheck(inst, at.Unix())
}

func (metricsListener) OnTransition(name string, from, to instance.Health, _ time.Time, _ string) {
	observability.IncInstanceHealthTransition(shareddomain.InstanceName(name), string(from), string(to))
}

func healthCode(h instance.Health) int {
	switch h {
	case instance.HealthAvailable:
		return 0
	case instance.HealthUnavailableAuth:
		return 1
	case instance.HealthUnavailableNetwork:
		return 2
	case instance.HealthSelfThrottled:
		return 4
	default:
		return 3
	}
}

// ReplaceClients swaps the client list the periodic preflight loop
// iterates AND reconciles the registry's membership with `names`.
// Called from the reload subscriber on every snapshot. The registry
// pointer is NOT reassigned — watchdog and scan UC keep their
// boot-time reference. The listener attached at construction is
// preserved.
//
// `names` is the source of truth for registry membership; `clients`
// is the source of truth for polling. In production the subscriber
// derives both from the same source so they always agree, but the
// two-arg signature makes the contract explicit.
func (c *Checker) ReplaceClients(clients []ports.SonarrClient, names []string) {
	cp := append([]ports.SonarrClient(nil), clients...)
	c.instances.Store(&cp)
	c.registry.SetNames(names)
}
