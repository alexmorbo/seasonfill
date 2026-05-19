package healthcheck

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

type Checker struct {
	db        *gorm.DB
	instances []ports.SonarrClient

	mu     sync.RWMutex
	health map[string]instance.Health
}

func New(db *gorm.DB, instances []ports.SonarrClient) *Checker {
	c := &Checker{
		db:        db,
		instances: instances,
		health:    make(map[string]instance.Health, len(instances)),
	}
	for _, inst := range instances {
		c.health[inst.Name()] = instance.Health{Name: inst.Name(), Status: instance.StatusUnknown}
	}
	return c
}

func (c *Checker) Preflight(ctx context.Context) {
	for _, inst := range c.instances {
		name := inst.Name()
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := inst.SystemStatus(callCtx)
		cancel()
		c.mu.Lock()
		if err != nil {
			c.health[name] = instance.Health{Name: name, Status: instance.StatusUnavailable, LastError: err.Error(), CheckedAt: time.Now().UTC()}
			observability.SetInstanceAvailable(name, false)
		} else {
			c.health[name] = instance.Health{Name: name, Status: instance.StatusAvailable, CheckedAt: time.Now().UTC()}
			observability.SetInstanceAvailable(name, true)
		}
		c.mu.Unlock()
	}
}

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

func (c *Checker) AnyInstanceAvailable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, h := range c.health {
		if h.Status == instance.StatusAvailable {
			return true
		}
	}
	return false
}

func (c *Checker) DatabaseUp(ctx context.Context) bool {
	sqlDB, err := c.db.DB()
	if err != nil {
		return false
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return sqlDB.PingContext(pctx) == nil
}

func (c *Checker) Snapshot() []instance.Health {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]instance.Health, 0, len(c.health))
	for _, h := range c.health {
		out = append(out, h)
	}
	return out
}
