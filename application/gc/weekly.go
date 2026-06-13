// Package gc holds the weekly garbage collection sweep for the
// entity-model layer (story 218 E-2). Schedule: Sunday 05:00 via the
// scheduler registry. The orchestrator runs three sub-tasks
// sequentially — orphan-series → media-sweep → event-prune — each
// best-effort: a sub-task error WARN-logs and the next still runs.
//
// Why sequential, not parallel: total runtime is < 30s on a typical
// library (300 series, 5000 media_assets). Saving 10s by running
// them in goroutines isn't worth the error-handling complexity.

package gc

import (
	"context"
	"log/slog"
	"time"
)

// WeeklyJob is the dependency bundle. Every sub-task is OPTIONAL —
// nil means "skip this slice this week" (used in tests and during
// the A-* deferral for EventPrune).
type WeeklyJob struct {
	OrphanSeries func(ctx context.Context) (OrphanSeriesResult, error)
	MediaSweep   func(ctx context.Context) (MediaSweepResult, error)
	EventPrune   func(ctx context.Context) (EventPruneResult, error)
	Clock        func() time.Time
	Logger       *slog.Logger
}

// Run is the cron entrypoint. The scheduler registers this under
// "weekly-gc" with schedule "0 5 * * 0".
func (j WeeklyJob) Run(ctx context.Context) {
	log := j.Logger
	if log == nil {
		log = slog.Default()
	}
	clock := j.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	start := clock()
	log.InfoContext(ctx, "weekly-gc.started", slog.Time("started_at", start))

	if j.OrphanSeries != nil {
		if r, err := j.OrphanSeries(ctx); err != nil {
			log.WarnContext(ctx, "weekly-gc.orphan_series.failed",
				slog.String("error", err.Error()))
		} else {
			log.InfoContext(ctx, "weekly-gc.orphan_series.ok",
				slog.Int("candidates", r.Candidates),
				slog.Int("deleted", r.Deleted),
			)
		}
	}
	if j.MediaSweep != nil {
		if r, err := j.MediaSweep(ctx); err != nil {
			log.WarnContext(ctx, "weekly-gc.media_sweep.failed",
				slog.String("error", err.Error()))
		} else {
			log.InfoContext(ctx, "weekly-gc.media_sweep.ok",
				slog.Int("live_hashes", r.LiveHashCount),
				slog.Int("candidates", r.Candidates),
				slog.Int("deleted", r.Deleted),
				slog.Int("store_failures", r.StoreFailures),
			)
		}
	}
	if j.EventPrune != nil {
		if r, err := j.EventPrune(ctx); err != nil {
			log.WarnContext(ctx, "weekly-gc.event_prune.failed",
				slog.String("error", err.Error()))
		} else if r.Skipped {
			log.InfoContext(ctx, "weekly-gc.event_prune.skipped",
				slog.String("reason", r.SkipReason))
		} else {
			log.InfoContext(ctx, "weekly-gc.event_prune.ok",
				slog.Int("deleted", r.Deleted),
			)
		}
	}

	log.InfoContext(ctx, "weekly-gc.finished",
		slog.Duration("elapsed", clock().Sub(start)))
}

// OrphanSeriesResult is the orphan-series sweep outcome.
type OrphanSeriesResult struct {
	Candidates int
	Deleted    int
}

// MediaSweepResult is the media-sweep outcome.
type MediaSweepResult struct {
	LiveHashCount int
	Candidates    int
	Deleted       int
	StoreFailures int
}

// EventPruneResult is the event-prune outcome. Skipped=true with a
// non-empty SkipReason indicates a deferred path (e.g., A-3 hasn't
// landed yet).
type EventPruneResult struct {
	Deleted    int
	Skipped    bool
	SkipReason string
}
