// movie_refresh_scheduler.go — Ф6-R-4a (L3-2) SEPARATE movie refresh scheduler.
//
// A near-copy of RefreshScheduler.Tick with a movie picker + movie worker + its
// OWN BatchSize budget + OWN ticker + movie-scoped metrics. This is the budget-
// isolation guarantee: a movie tick calls PickMovieRefreshCandidates (over the
// `movies` table) and MovieWorker.HandleForced ONLY — it never dequeues series
// candidates, and a series tick never dequeues movie candidates. Copy (not
// genericise-the-existing-scheduler) is deliberate: parameterising the series
// RefreshScheduler would edit TV code and risk a byte-identity regression.
package enrichment

import (
	"context"
	"errors"
	"log/slog"
	"time"

	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieRefreshSchedulerDeps is the construction surface. Required: Picker,
// Worker. BatchSize / TTL / Metrics / Logger / Clock default.
type MovieRefreshSchedulerDeps struct {
	Picker    MovieRefreshPicker
	Worker    MovieForceRefresher
	BatchSize int
	TTL       enrichdomain.RefreshTTL
	Metrics   MovieRefreshMetrics
	Logger    *slog.Logger
	Clock     func() time.Time
}

// MovieRefreshScheduler is the constructed movie scheduler. Tick is reentrant-
// safe via inFlight — a slow tick on a TMDB outage cannot overlap the next wake.
type MovieRefreshScheduler struct {
	deps     MovieRefreshSchedulerDeps
	inFlight chan struct{}
}

// NewMovieRefreshScheduler validates required deps. Returns error rather than
// panicking so the boot wirer can surface a "movie scheduler disabled" line.
func NewMovieRefreshScheduler(deps MovieRefreshSchedulerDeps) (*MovieRefreshScheduler, error) {
	if deps.Picker == nil {
		return nil, errors.New("movie refresh scheduler: Picker is required")
	}
	if deps.Worker == nil {
		return nil, errors.New("movie refresh scheduler: Worker is required")
	}
	if deps.BatchSize <= 0 {
		deps.BatchSize = 50
	}
	if (deps.TTL == enrichdomain.RefreshTTL{}) {
		deps.TTL = enrichdomain.DefaultRefreshTTL()
	}
	if deps.Metrics == nil {
		deps.Metrics = noopMovieRefreshMetrics{}
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &MovieRefreshScheduler{
		deps:     deps,
		inFlight: make(chan struct{}, 1),
	}, nil
}

// Tick runs ONE movie refresh sweep. Picker → serial HandleForced → metrics.
// Reentrant-safe: a second concurrent Tick returns immediately. A worker error
// does NOT abort the batch — each movie is independent; log + count + continue.
func (s *MovieRefreshScheduler) Tick(ctx context.Context) {
	select {
	case s.inFlight <- struct{}{}:
		defer func() { <-s.inFlight }()
	default:
		s.deps.Logger.InfoContext(ctx, "enrichment.movie_refresh.tick.skipped",
			slog.String("reason", "in_flight"),
		)
		return
	}

	start := s.deps.Clock()
	defer func() {
		s.deps.Metrics.ObserveTickDuration(s.deps.Clock().Sub(start))
	}()

	candidates, err := s.deps.Picker.PickMovieRefreshCandidates(ctx, start, s.deps.TTL, s.deps.BatchSize)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "enrichment.movie_refresh.pick_failed",
			slog.String("error", err.Error()),
		)
		return
	}
	s.deps.Metrics.ObserveBatchSize(len(candidates))
	if len(candidates) == 0 {
		s.deps.Logger.DebugContext(ctx, "enrichment.movie_refresh.tick.empty")
		return
	}

	changed := 0
	for _, c := range candidates {
		if c.Tier == enrichdomain.RefreshTierChanged {
			changed++
		}
	}
	s.deps.Logger.InfoContext(ctx, "enrichment.movie_refresh.tick.start",
		slog.Int("batch_size", len(candidates)),
		slog.Int("changed", changed),
	)

	var (
		okCount      int
		errCount     int
		skippedCount int
	)
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			s.deps.Logger.InfoContext(ctx, "enrichment.movie_refresh.tick.cancelled",
				slog.Int("processed", okCount+errCount),
				slog.Int("remaining", len(candidates)-(okCount+errCount+skippedCount)),
			)
			return
		}
		err := s.deps.Worker.HandleForced(ctx, c.MovieID)
		switch {
		case err == nil:
			okCount++
			s.deps.Metrics.IncRefresh(c.Tier, "ok")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			skippedCount++
			s.deps.Metrics.IncRefresh(c.Tier, "skipped")
		default:
			errCount++
			s.deps.Metrics.IncRefresh(c.Tier, "error")
			s.deps.Logger.WarnContext(ctx, "enrichment.movie_refresh.movie_failed",
				slog.Int64("movie_id", c.MovieID),
				slog.String("tier", c.Tier.String()),
				slog.String("error", err.Error()),
			)
		}
	}
	s.deps.Logger.InfoContext(ctx, "enrichment.movie_refresh.tick.done",
		slog.Int("ok", okCount),
		slog.Int("error", errCount),
		slog.Int("skipped", skippedCount),
	)
}

// RunForever blocks until ctx is cancelled, ticking every `interval`. The FIRST
// tick fires IMMEDIATELY (cold-start contract matches RefreshScheduler.RunForever).
func (s *MovieRefreshScheduler) RunForever(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
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
