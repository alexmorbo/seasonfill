package loops

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// DefaultEnrichmentCoverageInterval is the cadence for the M-8 backfill
// coverage-detail collector. Coverage moves only as enrichment / backfill
// lands rows — minutes-scale, never sub-minute — so 5 minutes keeps the
// bounded aggregates off the hot path while refreshing the gauges within an
// enrichment window. Same cadence as the sibling coverage collectors.
const DefaultEnrichmentCoverageInterval = 5 * time.Minute

// EnrichmentCoverageRepo is the narrow repo surface the collector needs.
// *enrichment/persistence.EnrichmentCoverageRepository satisfies it.
type EnrichmentCoverageRepo interface {
	EnrichmentCoverage(ctx context.Context) (enrichpersistence.EnrichmentCoverage, error)
}

// EnrichmentCoverageMetrics is the narrow metric port. Production:
// observability.EnrichmentCoverageMetricsAdapter.
type EnrichmentCoverageMetrics interface {
	SetPosterCoverageRatio(lang string, ratio float64)
	SetCheckedEmpty(kind string, n int64)
	SetUnenrichedSeries(reason string, n int64)
}

// EnrichmentCoverageLoop is the 5-minute backfill coverage-detail collector.
// One goroutine, drained on ctx cancel via bgWG. Per-tick failures are logged
// WARN and never propagate — the gauges survive a missed tick.
type EnrichmentCoverageLoop struct {
	repo    EnrichmentCoverageRepo
	metrics EnrichmentCoverageMetrics
	bgWG    *sync.WaitGroup
	logger  *slog.Logger

	intervalNS atomic.Int64
}

// NewEnrichmentCoverageLoop wires the collector. interval <= 0 → default.
// log nil → DomainLogger("enrichment_coverage").
func NewEnrichmentCoverageLoop(
	repo EnrichmentCoverageRepo,
	metricsPort EnrichmentCoverageMetrics,
	interval time.Duration,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) *EnrichmentCoverageLoop {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "enrichment_coverage")
	}
	if interval <= 0 {
		interval = DefaultEnrichmentCoverageInterval
	}
	l := &EnrichmentCoverageLoop{
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
func (l *EnrichmentCoverageLoop) Run(ctx context.Context) {
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

// tick runs the coverage-detail queries once and publishes every gauge. A
// query failure is logged WARN and skips this tick entirely (partial
// publication would leave stale/mixed gauges). LibraryTotal==0 → ratio 1.0
// (vacuously complete).
func (l *EnrichmentCoverageLoop) tick(ctx context.Context) {
	if l.repo == nil || l.metrics == nil {
		return
	}
	c, err := l.repo.EnrichmentCoverage(ctx)
	if err != nil {
		l.logger.WarnContext(ctx, "enrichment_coverage_query_failed",
			slog.String("error", err.Error()))
		return
	}
	for _, lang := range locale.SupportedUserLanguages {
		ratio := 1.0
		if c.LibraryTotal > 0 {
			ratio = float64(c.PosterCoveredByLang[lang]) / float64(c.LibraryTotal)
		}
		l.metrics.SetPosterCoverageRatio(lang, ratio)
	}
	for _, kind := range []string{"poster", "backdrop"} {
		l.metrics.SetCheckedEmpty(kind, c.CheckedEmpty[kind])
	}
	for _, reason := range []string{"no_tmdb_id", "never_synced"} {
		l.metrics.SetUnenrichedSeries(reason, c.Unenriched[reason])
	}
}
