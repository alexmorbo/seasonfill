package adapters

import (
	"context"

	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/enrichment/rest/seriesrefresh"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesRefreshSeriesAdapter projects SeriesRepository.Get onto the
// thin seriesrefresh.CanonView shape so the use case stays free of
// the domain/series import. Story 218 (E-2).
type SeriesRefreshSeriesAdapter struct {
	R *persistence.SeriesRepository
}

// NewSeriesRefreshSeriesAdapter wraps the supplied repository.
func NewSeriesRefreshSeriesAdapter(r *persistence.SeriesRepository) SeriesRefreshSeriesAdapter {
	return SeriesRefreshSeriesAdapter{R: r}
}

// Assert interface satisfaction at compile time.
var _ seriesrefresh.SeriesByIDReader = SeriesRefreshSeriesAdapter{}

// Get implements seriesrefresh.SeriesByIDReader.
func (a SeriesRefreshSeriesAdapter) Get(ctx context.Context, id domain.SeriesID) (seriesrefresh.CanonView, error) {
	c, err := a.R.Get(ctx, id)
	if err != nil {
		return seriesrefresh.CanonView{}, err
	}
	return seriesrefresh.CanonView{ID: c.ID, IMDBID: c.IMDBID}, nil
}

// SeriesRefreshCastAdapter implements seriesrefresh.TopCastReader by
// calling SeriesPeopleRepository.ListBySeries (the composer's existing
// path) and slicing the first N person ids. Story 218 (E-2).
type SeriesRefreshCastAdapter struct {
	R *persistence.SeriesPeopleRepository
}

// NewSeriesRefreshCastAdapter wraps the supplied repository.
func NewSeriesRefreshCastAdapter(r *persistence.SeriesPeopleRepository) SeriesRefreshCastAdapter {
	return SeriesRefreshCastAdapter{R: r}
}

// Assert interface satisfaction at compile time.
var _ seriesrefresh.TopCastReader = SeriesRefreshCastAdapter{}

// TopCastPersonIDs implements seriesrefresh.TopCastReader.
func (a SeriesRefreshCastAdapter) TopCastPersonIDs(ctx context.Context, seriesID domain.SeriesID, limit int) ([]int64, error) {
	credits, err := a.R.ListBySeries(ctx, seriesID, dompeople.SeriesCreditCast)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > len(credits) {
		limit = len(credits)
	}
	out := make([]int64, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, credits[i].PersonID)
	}
	return out, nil
}
