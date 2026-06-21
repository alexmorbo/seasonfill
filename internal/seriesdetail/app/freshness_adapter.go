package seriesdetail

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// EnrichmentFreshnessAdapter wires SeriesRepository + EnrichmentErrorsRepository
// behind the EnrichmentFreshnessPort the composer reads through. Per-source
// SyncedAtFor resolves to the canon row's enrichment_*_synced_at column
// (TMDB series + OMDb cases); ErrorsFor delegates straight to the
// enrichment_errors repo.
//
// Sources without a canon-tracked column (tmdb_season, tmdb_person — those
// don't live on the series canon row) return nil → the composer's degraded
// rule 1 fires, surfacing "never synced" until the per-entity column lands
// in a future schema iteration.
type EnrichmentFreshnessAdapter struct {
	series *enrichpersistence.SeriesRepository
	errors *enrichpersistence.EnrichmentErrorsRepository
}

// NewEnrichmentFreshnessAdapter constructs the adapter. Both repos are
// required.
func NewEnrichmentFreshnessAdapter(series *enrichpersistence.SeriesRepository, errors *enrichpersistence.EnrichmentErrorsRepository) *EnrichmentFreshnessAdapter {
	return &EnrichmentFreshnessAdapter{series: series, errors: errors}
}

// SyncedAtFor returns the last-success timestamp for the requested
// source on the canon series. TMDB series + OMDb read the canon row's
// dedicated column; other sources return nil (composer interprets nil
// as "never enriched" which is the correct default for sources that
// don't live on the series canon row).
func (a *EnrichmentFreshnessAdapter) SyncedAtFor(ctx context.Context, seriesID domain.SeriesID, source enrichment.Source) (*time.Time, error) {
	canon, err := a.series.Get(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	switch source {
	case enrichment.SourceTMDBSeries:
		return canon.EnrichmentTMDBSyncedAt, nil
	case enrichment.SourceOMDb:
		return canon.EnrichmentOMDBSyncedAt, nil
	default:
		return nil, nil
	}
}

// ErrorsFor returns every live enrichment_errors row for the canon
// series across all sources. The composer filters per-source for the
// degraded[] live-error rule.
func (a *EnrichmentFreshnessAdapter) ErrorsFor(ctx context.Context, seriesID domain.SeriesID) ([]enrichment.EnrichmentError, error) {
	return a.errors.GetForEntity(ctx, enrichment.EntityTypeSeries, int64(seriesID))
}
