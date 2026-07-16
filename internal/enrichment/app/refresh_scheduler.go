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
	// MissingPoster is true only when the picker selected this row
	// EXCLUSIVELY via the W17-1 HOT poster-guard branch — a TTL-fresh
	// library series with no series_media_texts.poster_asset. A
	// poster-less row that was already due via the normal TTL/staleness
	// gate is a normal HOT pick and carries MissingPoster=false. Drives
	// the observability signal for the one-shot backfill drain.
	MissingPoster bool
	// Heal is true when the picker selected this row via the #1090b
	// null-heal branch (tv-row person_credits all NULL last_appearance_season).
	// Non-exclusive of MissingPoster / normal picks. Drives the
	// seasonfill_enrichment_refresh_picked_heal_total observability signal —
	// its steady-state rate is the genuinely-unfillable (crew/specials-only)
	// floor.
	Heal bool
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
	// IncRefreshPickedMissingPoster ticks once per candidate the picker
	// selected via the W17-1 poster-guard branch. Lets Grafana watch the
	// backfill drain toward the tmdb-less floor.
	IncRefreshPickedMissingPoster()
	// IncRefreshPickedHeal ticks once per candidate the picker selected via
	// the #1090b null-heal branch. Its steady-state rate is the genuinely-
	// unfillable (crew-only / specials-only) floor that re-picks every 6h.
	IncRefreshPickedHeal()
}

// noopRefreshMetrics is the zero-value default so an unconfigured
// metrics port doesn't panic. Used in tests that don't care.
type noopRefreshMetrics struct{}

func (noopRefreshMetrics) IncRefresh(enrichdomain.RefreshTier, string) {}
func (noopRefreshMetrics) ObserveBatchSize(int)                        {}
func (noopRefreshMetrics) ObserveTickDuration(time.Duration)           {}
func (noopRefreshMetrics) IncRefreshPickedMissingPoster()              {}
func (noopRefreshMetrics) IncRefreshPickedHeal()                       {}

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

	// W17-1 / #1090b — surface the pick reasons. Count the poster-guard picks
	// and the null-heal picks, emitting one metric tick + a per-series log line
	// per reason. A row can qualify for both (F-05 overlap), so each flag is
	// checked independently — the two counters are intentionally non-exclusive.
	// W2-5 — additionally tally CHANGED tier-0 picks (TMDB /tv/changes) for the
	// tick.start log; no per-series log or metric (would be noise, out of scope).
	missingPoster := 0
	heal := 0
	changed := 0
	for _, c := range candidates {
		if c.Tier == enrichdomain.RefreshTierChanged {
			changed++
		}
		if c.MissingPoster {
			missingPoster++
			s.deps.Metrics.IncRefreshPickedMissingPoster()
			s.deps.Logger.InfoContext(ctx, "enrichment.refresh.picked",
				slog.Int64("series_id", c.SeriesID),
				slog.String("reason", "missing_poster"),
			)
		}
		if c.Heal {
			heal++
			s.deps.Metrics.IncRefreshPickedHeal()
			s.deps.Logger.InfoContext(ctx, "enrichment.refresh.picked",
				slog.Int64("series_id", c.SeriesID),
				slog.String("reason", "heal"),
			)
		}
	}

	s.deps.Logger.InfoContext(ctx, "enrichment.refresh.tick.start",
		slog.Int("batch_size", len(candidates)),
		slog.Int("changed", changed),
		slog.Int("missing_poster", missingPoster),
		slog.Int("heal", heal),
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
