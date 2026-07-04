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

// DefaultLibraryPosterCoverageInterval is the cadence for the library poster
// coverage collector. Coverage moves only as enrichment / discovery-warming
// lands series_media_texts rows — minutes-scale, never sub-minute — so 5
// minutes keeps the single query off the hot path while keeping the gauges
// fresh within an enrichment window.
const DefaultLibraryPosterCoverageInterval = 5 * time.Minute

// LibraryPosterCoverageRepo is the narrow repo surface the collector needs.
// *catalog/persistence.LibraryPosterCoverageRepository satisfies it.
type LibraryPosterCoverageRepo interface {
	LibraryPosterCoverage(ctx context.Context) (catalogpersistence.LibraryPosterCoverage, error)
}

// LibraryPosterCoverageMetrics is the narrow metric port. Production:
// observability.LibraryPosterCoverageMetricsAdapter.
type LibraryPosterCoverageMetrics interface {
	SetLibraryPosterCoverage(covered, total int64)
}

// LibraryPosterCoverageLoop is the 5-minute library poster coverage
// collector. One goroutine, drained on ctx cancel via bgWG. Per-tick
// failures are logged WARN and never propagate — the gauges survive a
// missed tick.
type LibraryPosterCoverageLoop struct {
	repo    LibraryPosterCoverageRepo
	metrics LibraryPosterCoverageMetrics
	bgWG    *sync.WaitGroup
	logger  *slog.Logger

	intervalNS atomic.Int64
}

// NewLibraryPosterCoverageLoop wires the collector. interval <= 0 → default.
// log nil → DomainLogger("library_poster_coverage").
func NewLibraryPosterCoverageLoop(
	repo LibraryPosterCoverageRepo,
	metricsPort LibraryPosterCoverageMetrics,
	interval time.Duration,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) *LibraryPosterCoverageLoop {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "library_poster_coverage")
	}
	if interval <= 0 {
		interval = DefaultLibraryPosterCoverageInterval
	}
	l := &LibraryPosterCoverageLoop{
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
func (l *LibraryPosterCoverageLoop) Run(ctx context.Context) {
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

// tick runs the coverage query once and publishes the gauges. A query
// failure is logged WARN and skips this tick entirely (partial publication
// would leave stale/mixed gauges).
func (l *LibraryPosterCoverageLoop) tick(ctx context.Context) {
	if l.repo == nil || l.metrics == nil {
		return
	}
	c, err := l.repo.LibraryPosterCoverage(ctx)
	if err != nil {
		l.logger.WarnContext(ctx, "library_poster_coverage_query_failed",
			slog.String("error", err.Error()))
		return
	}
	l.metrics.SetLibraryPosterCoverage(c.Covered, c.Total)
}
