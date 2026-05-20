package healthcheck

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

type Checker struct {
	db        *gorm.DB
	instances []ports.SonarrClient
	registry  *instance.Registry
}

func New(db *gorm.DB, instances []ports.SonarrClient) *Checker {
	names := make([]string, 0, len(instances))
	for _, inst := range instances {
		names = append(names, inst.Name())
	}
	reg := instance.NewRegistry(names).WithListener(metricsListener{})
	return &Checker{db: db, instances: instances, registry: reg}
}

// Registry returns the underlying health registry. The watchdog and the
// `/api/v1/instances` handler (004d) share it.
func (c *Checker) Registry() *instance.Registry { return c.registry }

// Preflight loops every known instance and updates the registry.
func (c *Checker) Preflight(ctx context.Context) {
	for _, inst := range c.instances {
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
	for _, inst := range c.instances {
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

// metricsListener is a minimal shim that mirrors the legacy
// `SetInstanceAvailable` 0/1 gauge so existing dashboards keep working. 004d
// replaces this with the typed `seasonfill_instance_health` gauge.
type metricsListener struct{}

func (metricsListener) OnCheck(name string, h instance.Health, _ time.Time) {
	observability.SetInstanceAvailable(name, h == instance.HealthAvailable)
}

func (metricsListener) OnTransition(_ string, _, _ instance.Health, _ time.Time, _ string) {
}
