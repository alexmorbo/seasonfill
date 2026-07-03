package loops

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// DefaultCatalogCountsInterval is the cadence for the catalog-size
// collector. The counts move only as scans land rows — minutes-scale,
// never sub-minute — so 5 minutes keeps the three COUNT queries off the
// hot path while keeping the gauges fresh within a scan window.
const DefaultCatalogCountsInterval = 5 * time.Minute

// CatalogCountsRepo is the narrow repo surface the collector needs.
// *catalog/persistence.CatalogCountsRepository satisfies it.
type CatalogCountsRepo interface {
	Counts(ctx context.Context) (catalogpersistence.CatalogCounts, error)
}

// CatalogCountsMetrics is the narrow metric port. Production:
// observability.CatalogCountsMetricsAdapter.
type CatalogCountsMetrics interface {
	SetCatalogCounts(series, seasons, episodes int64)
}

// CatalogCountsLoop is the 5-minute catalog-size collector. One
// goroutine, drained on ctx cancel via bgWG. Per-tick failures are logged
// WARN and never propagate — the gauges survive a missed tick.
type CatalogCountsLoop struct {
	repo    CatalogCountsRepo
	metrics CatalogCountsMetrics
	bgWG    *sync.WaitGroup
	logger  *slog.Logger

	intervalNS atomic.Int64
}

// NewCatalogCountsLoop wires the collector. interval <= 0 → default.
// log nil → DomainLogger("catalog_counts").
func NewCatalogCountsLoop(
	repo CatalogCountsRepo,
	metricsPort CatalogCountsMetrics,
	interval time.Duration,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) *CatalogCountsLoop {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "catalog_counts")
	}
	if interval <= 0 {
		interval = DefaultCatalogCountsInterval
	}
	l := &CatalogCountsLoop{
		repo:    repo,
		metrics: metricsPort,
		bgWG:    bgWG,
		logger:  log,
	}
	l.intervalNS.Store(int64(interval))
	return l
}

// Run is the main loop. Blocks until ctx is cancelled. Callers should
// `go l.Run(ctx)` after bumping bgWG.Add(1); the loop calls bgWG.Done()
// on exit.
func (l *CatalogCountsLoop) Run(ctx context.Context) {
	if l.bgWG != nil {
		defer l.bgWG.Done()
	}
	// Immediate first tick so a fresh pod publishes the gauges without
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

// tick runs the three COUNT queries once and publishes the gauges. A
// query failure is logged WARN and skips this tick entirely (partial
// publication would leave stale/mixed gauges).
func (l *CatalogCountsLoop) tick(ctx context.Context) {
	if l.repo == nil || l.metrics == nil {
		return
	}
	c, err := l.repo.Counts(ctx)
	if err != nil {
		l.logger.WarnContext(ctx, "catalog_counts_query_failed",
			slog.String("error", err.Error()))
		return
	}
	l.metrics.SetCatalogCounts(c.Series, c.Seasons, c.Episodes)
}
