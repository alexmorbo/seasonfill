package loops

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// DefaultI18nCoverageInterval is the cadence for the base-lang coverage
// collector. Coverage moves only as TMDB enrichment / backfill lands rows —
// minutes-scale, never sub-minute — so 5 minutes keeps the five COUNT
// queries off the hot path while still refreshing the S-E3 gate signal
// well within a deploy window.
const DefaultI18nCoverageInterval = 5 * time.Minute

// I18nCoverageRepo is the narrow repo surface the collector needs.
// *enrichment/persistence.I18nCoverageRepository satisfies it.
type I18nCoverageRepo interface {
	BaseLangCoverage(ctx context.Context) ([]enrichpersistence.BaseCoverageRow, error)
}

// I18nCoverageMetrics is the narrow metric port. Production:
// observability.I18nCoverageMetricsAdapter.
type I18nCoverageMetrics interface {
	SetI18nBaseCoverage(table string, pct float64)
}

// I18nCoverageLoop is the 5-minute base-lang coverage collector. One
// goroutine, drained on ctx cancel via bgWG. Per-tick failures are logged
// WARN and never propagate — the gauge survives a missed tick.
type I18nCoverageLoop struct {
	repo    I18nCoverageRepo
	metrics I18nCoverageMetrics
	bgWG    *sync.WaitGroup
	logger  *slog.Logger

	intervalNS atomic.Int64
}

// NewI18nCoverageLoop wires the collector. interval <= 0 → default.
// log nil → DomainLogger("i18n_coverage").
func NewI18nCoverageLoop(
	repo I18nCoverageRepo,
	metricsPort I18nCoverageMetrics,
	interval time.Duration,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) *I18nCoverageLoop {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "i18n_coverage")
	}
	if interval <= 0 {
		interval = DefaultI18nCoverageInterval
	}
	l := &I18nCoverageLoop{
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
func (l *I18nCoverageLoop) Run(ctx context.Context) {
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

// tick runs the five coverage queries once and publishes each gauge. A
// query failure is logged WARN and skips this tick entirely (partial
// publication would leave stale/mixed gauges). Total==0 → 100.0 (vacuous).
func (l *I18nCoverageLoop) tick(ctx context.Context) {
	if l.repo == nil || l.metrics == nil {
		return
	}
	rows, err := l.repo.BaseLangCoverage(ctx)
	if err != nil {
		l.logger.WarnContext(ctx, "i18n_coverage_query_failed",
			slog.String("error", err.Error()))
		return
	}
	for _, row := range rows {
		pct := 100.0
		if row.Total > 0 {
			pct = float64(row.Covered) / float64(row.Total) * 100.0
		}
		l.metrics.SetI18nBaseCoverage(row.Table, pct)
	}
}
