package adapters

import (
	"context"

	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/enrichment/rest/seriesrefresh"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
)

// SeriesPeopleListPort is the narrow read surface
// SeriesRefreshCastAdapter consumes — the same shape the
// seriesdetail.SeriesPeoplePort exposes. *SeriesPeopleFromPersonCredits
// satisfies it; the refresh adapter delegates to the same D-7
// person_credits-backed path the composer reads from.
type SeriesPeopleListPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID, kind dompeople.SeriesCreditKind, lang string) ([]dompeople.SeriesCredit, error)
}

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
// delegating to a SeriesPeopleListPort (the same surface the
// composer reads). Story 218 (E-2); rewired to person_credits by
// D-7 (468a) — the legacy *SeriesPeopleRepository was dropped.
type SeriesRefreshCastAdapter struct {
	R SeriesPeopleListPort
}

// NewSeriesRefreshCastAdapter wraps the supplied list port. In
// production the input is *SeriesPeopleFromPersonCredits, which the
// seriesdetail wiring already constructs for the composer + cast
// composer.
func NewSeriesRefreshCastAdapter(r SeriesPeopleListPort) SeriesRefreshCastAdapter {
	return SeriesRefreshCastAdapter{R: r}
}

// Assert interface satisfaction at compile time.
var _ seriesrefresh.TopCastReader = SeriesRefreshCastAdapter{}

// TopCastPersonIDs implements seriesrefresh.TopCastReader.
func (a SeriesRefreshCastAdapter) TopCastPersonIDs(ctx context.Context, seriesID domain.SeriesID, limit int) ([]int64, error) {
	// TopCastPersonIDs needs person IDs only — character-name localization is
	// irrelevant here, so read the base language tier (locale.Default()).
	credits, err := a.R.ListBySeries(ctx, seriesID, dompeople.SeriesCreditCast, locale.Default())
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
