// Story 534 — Background refresh scheduler.
//
// RefreshScheduler runs every 30 minutes (configurable for tests),
// picks up to BatchSize stale series via the tiered SQL picker
// (HOT → NORMAL → COLD), and invokes SeriesWorker.HandleForced on
// each one SERIALLY. Serial execution is correct because:
//
//   - tmdb.Client owns adaptive rate-limit pause; parallel callers
//     just queue behind it and gain nothing.
//   - A typical batch is 50 × ~200ms ≈ 10s, well inside the 30min
//     tick budget.
//   - Singleflight at the scheduler entry guarantees no two ticks
//     overlap even on a TMDB outage (which can stretch a tick to
//     minutes via the worker's per-language retry).
//
// Force semantics: the scheduler ALWAYS calls HandleForced — the whole
// point of running every 30min is to bypass Handle's in-band TTL
// short-circuit (series_worker.go:165). The picker's own TTL
// (RefreshTTL.For(tier)) is the staleness gate; the worker's TTL is
// not re-checked.
package enrichment

import (
	"context"
	"errors"
	"log/slog"
	"time"

	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// RefreshCandidate mirrors the persistence DTO; redeclared here so the
// app package owns its own picker port without leaking GORM types
// upward. Persistence package's RefreshCandidate satisfies this via a
// trivial adapter in wiring.
type RefreshCandidate struct {
	SeriesID int64
	Tier     enrichdomain.RefreshTier
}

// RefreshPicker is the port the scheduler depends on. Production
// implementation wraps SeriesRepository.PickRefreshCandidates.
type RefreshPicker interface {
	PickRefreshCandidates(ctx context.Context, now time.Time, ttl enrichdomain.RefreshTTL, limit int) ([]RefreshCandidate, error)
}

// SeriesForceRefresher is the worker port. Production: *SeriesWorker
// via SeriesWorker.HandleForced.
type SeriesForceRefresher interface {
	HandleForced(ctx context.Context, seriesID int64) error
}

// RefreshMetrics is the narrow metric port. Production:
// observability.EnrichmentRefreshMetrics. Tests pass a recording fake.
type RefreshMetrics interface {
	IncRefresh(tier enrichdomain.RefreshTier, result string)
	ObserveBatchSize(n int)
	ObserveTickDuration(d time.Duration)
}

// noopRefreshMetrics is the zero-value default so an unconfigured
// metrics port doesn't panic. Used in tests that don't care.
type noopRefreshMetrics struct{}

func (noopRefreshMetrics) IncRefresh(enrichdomain.RefreshTier, string) {}
func (noopRefreshMetrics) ObserveBatchSize(int)                        {}
func (noopRefreshMetrics) ObserveTickDuration(time.Duration)           {}

// RefreshSchedulerDeps is the construction surface. Required:
// Picker, Worker. Logger defaults to ports.DomainLogger(slog.Default(),
// "enrichment"). Metrics defaults to noopRefreshMetrics. Clock and
// TTL default to time.Now().UTC and DefaultRefreshTTL.
type RefreshSchedulerDeps struct {
	Picker    RefreshPicker
	Worker    SeriesForceRefresher
	BatchSize int
	TTL       enrichdomain.RefreshTTL
	Metrics   RefreshMetrics
	Logger    *slog.Logger
	Clock     func() time.Time
}

// RefreshScheduler is the constructed scheduler. Tick is reentrant-
// safe via the inFlight channel — a slow tick on a TMDB outage cannot
// overlap with the next 30-min wake.
type RefreshScheduler struct {
	deps     RefreshSchedulerDeps
	inFlight chan struct{}
}

// NewRefreshScheduler validates required deps. Returns error rather
// than panicking so the boot wirer can surface a "scheduler disabled"
// log line when prerequisites are missing.
func NewRefreshScheduler(deps RefreshSchedulerDeps) (*RefreshScheduler, error) {
	if deps.Picker == nil {
		return nil, errors.New("refresh scheduler: Picker is required")
	}
	if deps.Worker == nil {
		return nil, errors.New("refresh scheduler: Worker is required")
	}
	if deps.BatchSize <= 0 {
		deps.BatchSize = 50
	}
	if (deps.TTL == enrichdomain.RefreshTTL{}) {
		deps.TTL = enrichdomain.DefaultRefreshTTL()
	}
	if deps.Metrics == nil {
		deps.Metrics = noopRefreshMetrics{}
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &RefreshScheduler{
		deps:     deps,
		inFlight: make(chan struct{}, 1),
	}, nil
}

// Tick runs ONE refresh sweep. Picker → serial HandleForced → metrics.
// Reentrant-safe: a second concurrent Tick returns immediately with
// "in_flight" log line and zero work. Errors from the worker do NOT
// abort the batch — each series is independent; we log + count and
// continue.
func (s *RefreshScheduler) Tick(ctx context.Context) {
	select {
	case s.inFlight <- struct{}{}:
		defer func() { <-s.inFlight }()
	default:
		s.deps.Logger.InfoContext(ctx, "enrichment.refresh.tick.skipped",
			slog.String("reason", "in_flight"),
		)
		return
	}

	start := s.deps.Clock()
	defer func() {
		s.deps.Metrics.ObserveTickDuration(s.deps.Clock().Sub(start))
	}()

	candidates, err := s.deps.Picker.PickRefreshCandidates(ctx, start, s.deps.TTL, s.deps.BatchSize)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "enrichment.refresh.pick_failed",
			slog.String("error", err.Error()),
		)
		return
	}
	s.deps.Metrics.ObserveBatchSize(len(candidates))
	if len(candidates) == 0 {
		s.deps.Logger.DebugContext(ctx, "enrichment.refresh.tick.empty")
		return
	}

	s.deps.Logger.InfoContext(ctx, "enrichment.refresh.tick.start",
		slog.Int("batch_size", len(candidates)),
	)

	var (
		okCount      int
		errCount     int
		skippedCount int
	)
	for _, c := range candidates {
		// Honour shutdown — caller's ctx cancellation wins over the
		// remaining batch. Already-running worker call drains via
		// HandleForced's own ctx propagation.
		if err := ctx.Err(); err != nil {
			s.deps.Logger.InfoContext(ctx, "enrichment.refresh.tick.cancelled",
				slog.Int("processed", okCount+errCount),
				slog.Int("remaining", len(candidates)-(okCount+errCount+skippedCount)),
			)
			return
		}
		err := s.deps.Worker.HandleForced(ctx, c.SeriesID)
		switch {
		case err == nil:
			okCount++
			s.deps.Metrics.IncRefresh(c.Tier, "ok")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// Treated as skipped, not error — the worker did nothing
			// substantive because the ctx died.
			skippedCount++
			s.deps.Metrics.IncRefresh(c.Tier, "skipped")
		default:
			errCount++
			s.deps.Metrics.IncRefresh(c.Tier, "error")
			s.deps.Logger.WarnContext(ctx, "enrichment.refresh.series_failed",
				slog.Int64("series_id", c.SeriesID),
				slog.String("tier", c.Tier.String()),
				slog.String("error", err.Error()),
			)
		}
	}
	s.deps.Logger.InfoContext(ctx, "enrichment.refresh.tick.done",
		slog.Int("ok", okCount),
		slog.Int("error", errCount),
		slog.Int("skipped", skippedCount),
	)
}

// RunForever blocks until ctx is cancelled, ticking every `interval`.
// Cold-start contract matches RunDiscovery: the FIRST tick fires
// IMMEDIATELY so a fresh pod populates without waiting 30 minutes.
func (s *RefreshScheduler) RunForever(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	// Immediate first tick.
	s.Tick(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(ctx)
		}
	}
}
