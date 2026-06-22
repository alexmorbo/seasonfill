package loops

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// DefaultQbitCapacityInterval is the cadence for the qbit_torrents row
// count collector. 60s is a balance: capacity does not change minute-
// to-minute, and the gauge survives missed scrapes (Prometheus stale
// marker fires at 5 × scrape interval). Slower than this and the
// "instance grew explosively" alert fires too late; faster and the
// per-instance COUNT(*) query starts noticeable on a 100k-row table.
const DefaultQbitCapacityInterval = 60 * time.Second

// QbitCapacityRepo is the narrow repo surface the collector needs.
// *catalog/persistence.QbitTorrentsRepository satisfies it implicitly
// via CountPresentByInstance.
type QbitCapacityRepo interface {
	CountPresentByInstance(ctx context.Context, instance domain.InstanceName) (int, error)
}

// QbitCapacityInstances is the registry surface. Production wiring
// closes over the SonarrBundle holder snapshot so a publish makes
// the next tick see the new instance set.
type QbitCapacityInstances interface {
	List() []domain.InstanceName
}

// QbitCapacityInstancesFunc is the func adapter — production wires a
// closure over holder.Load() through this type.
type QbitCapacityInstancesFunc func() []domain.InstanceName

// List implements QbitCapacityInstances.
func (f QbitCapacityInstancesFunc) List() []domain.InstanceName { return f() }

// QbitCapacityMetrics is the narrow metric port. Production:
// observability.QbitCapacityMetricsAdapter.
type QbitCapacityMetrics interface {
	SetQbitTorrentsRows(instance domain.InstanceName, count int)
}

// QbitCapacityLoop is the 60s row-count collector for the
// qbit_torrents table. One goroutine, drained on ctx cancel via
// bgWG. Per-tick failures are logged WARN but never propagate.
type QbitCapacityLoop struct {
	repo      QbitCapacityRepo
	instances QbitCapacityInstances
	metrics   QbitCapacityMetrics
	bgWG      *sync.WaitGroup
	logger    *slog.Logger

	intervalNS atomic.Int64
}

// NewQbitCapacityLoop wires the collector. interval <= 0 → default.
// log nil → DomainLogger("qbit").
func NewQbitCapacityLoop(
	repo QbitCapacityRepo,
	instances QbitCapacityInstances,
	metricsPort QbitCapacityMetrics,
	interval time.Duration,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) *QbitCapacityLoop {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "qbit")
	}
	if interval <= 0 {
		interval = DefaultQbitCapacityInterval
	}
	l := &QbitCapacityLoop{
		repo:      repo,
		instances: instances,
		metrics:   metricsPort,
		bgWG:      bgWG,
		logger:    log,
	}
	l.intervalNS.Store(int64(interval))
	return l
}

// Run is the main loop. Blocks until ctx is cancelled. Callers should
// `go l.Run(ctx)` after bumping bgWG.Add(1); the loop calls
// bgWG.Done() on exit.
func (l *QbitCapacityLoop) Run(ctx context.Context) {
	if l.bgWG != nil {
		defer l.bgWG.Done()
	}
	// Immediate first tick so a fresh pod has gauges populated without
	// waiting one full interval.
	l.tick(ctx)
	timer := time.NewTimer(time.Duration(l.intervalNS.Load()))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			l.tick(ctx)
			timer.Reset(time.Duration(l.intervalNS.Load()))
		}
	}
}

// tick iterates the instance list once and publishes the gauge for
// each. A per-instance error is logged WARN but does not abort the
// iteration — one bad instance must not stall the rest.
func (l *QbitCapacityLoop) tick(ctx context.Context) {
	if l.instances == nil || l.repo == nil || l.metrics == nil {
		return
	}
	for _, inst := range l.instances.List() {
		count, err := l.repo.CountPresentByInstance(ctx, inst)
		if err != nil {
			l.logger.WarnContext(ctx, "qbit_capacity_count_failed",
				slog.String("instance_name", string(inst)),
				slog.String("error", err.Error()))
			continue
		}
		l.metrics.SetQbitTorrentsRows(inst, count)
	}
}
