package healthcheck

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

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

// Preflight loops every known instance and updates the registry.
func (c *Checker) Preflight(ctx context.Context) {
	clients := *c.instances.Load()
	for _, inst := range clients {
		c.checkOne(ctx, inst)
	}
}

func (c *Checker) checkOne(ctx context.Context, client ports.SonarrClient) {
	name := client.Name()
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err := client.SystemStatus(callCtx)
	cancel()
	now := time.Now().UTC()
	if err == nil {
		c.registry.MarkAvailable(name, now)
		observability.SetInstanceAvailable(name, true)
		return
	}
	state := instance.HealthUnavailableUnknown
	switch {
	case errors.Is(err, domain.ErrInstanceUnauthorized):
		state = instance.HealthUnavailableAuth
	case errors.Is(err, domain.ErrInstanceNetwork):
		state = instance.HealthUnavailableNetwork
	}
	c.registry.MarkUnavailable(name, state, err.Error(), now)
	observability.SetInstanceAvailable(name, false)
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
	observability.SetInstanceAvailable(name, h == instance.HealthAvailable)
	observability.SetInstanceHealth(name, healthCode(h))
	observability.SetInstanceLastCheck(name, at.Unix())
}

func (metricsListener) OnTransition(name string, from, to instance.Health, _ time.Time, _ string) {
	observability.IncInstanceHealthTransition(name, string(from), string(to))
}

func healthCode(h instance.Health) int {
	switch h {
	case instance.HealthAvailable:
		return 0
	case instance.HealthUnavailableAuth:
		return 1
	case instance.HealthUnavailableNetwork:
		return 2
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
