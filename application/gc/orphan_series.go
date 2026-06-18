// orphan_series.go — story 218 E-2.
//
// Orphan canonical `series` rows are deleted when ALL of:
//   1. no live (deleted_at IS NULL) series_cache reference, AND
//   2. no series_recommendations reference (as recommended_series_id), AND
//   3. created_at < now - 90d (PRD §5.8 grace).
//
// Deletion is HARD — the canon row goes. Texts / seasons / episodes /
// people-joins / taxonomy joins are dropped by the application via
// the DropSeriesCascade helper. We do NOT FK at the DB level (PRD
// §5.4 — application-side cascades are the project's convention).

package gc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// OrphanSeriesDeps are the ports the sweep needs.
type OrphanSeriesDeps struct {
	Repo   OrphanSeriesRepo
	Clock  func() time.Time
	Logger *slog.Logger
	// GraceDuration overrides the default 90d for tests. Zero ⇒ 90d.
	GraceDuration time.Duration
}

// OrphanSeriesRepo is the narrow port the sweep consumes.
type OrphanSeriesRepo interface {
	// ListOrphanCandidates returns series.id rows that have NO live
	// series_cache reference AND NO series_recommendations reference
	// AND created_at < cutoff. Sweep callers pass cutoff = now - grace.
	ListOrphanCandidates(ctx context.Context, cutoff time.Time, limit int) ([]domain.SeriesID, error)
	// DropSeriesCascade hard-deletes one canon series + every
	// dependent row across the entity model. Single transaction;
	// idempotent (DELETE of a non-existent row is a no-op).
	DropSeriesCascade(ctx context.Context, seriesID domain.SeriesID) error
}

// Build constructs the WeeklyJob.OrphanSeries closure with the deps
// captured. The closure signature matches WeeklyJob's field type.
func (d OrphanSeriesDeps) Build() func(ctx context.Context) (OrphanSeriesResult, error) {
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	grace := d.GraceDuration
	if grace == 0 {
		grace = 90 * 24 * time.Hour
	}
	return func(ctx context.Context) (OrphanSeriesResult, error) {
		const sweepLimit = 1000
		cutoff := clock().Add(-grace)
		ids, err := d.Repo.ListOrphanCandidates(ctx, cutoff, sweepLimit)
		if err != nil {
			return OrphanSeriesResult{}, fmt.Errorf("list orphan candidates: %w", err)
		}
		res := OrphanSeriesResult{Candidates: len(ids)}
		for _, id := range ids {
			if derr := d.Repo.DropSeriesCascade(ctx, id); derr != nil {
				log.WarnContext(ctx, "orphan_series.drop_failed",
					slog.Int64("series_id", int64(id)),
					slog.String("error", derr.Error()))
				continue
			}
			res.Deleted++
		}
		return res, nil
	}
}
