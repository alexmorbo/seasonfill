package persistence

import (
	"context"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesPeopleRepository was the persistence wrapper around the
// legacy `series_people` table. The table was dropped in D-1 — series-
// level credits are now stored in `person_credits` with
// media_type='tv_series', tmdb_media_id=<series tmdb_id>.
//
// D-3 (story 464c) deletes the backing schema but the consumer rewrite
// (seriesdetail composer + cast composer + cmd/server series_refresh
// adapter) is owned by D-7. Until then every method panics with the
// canonical "pending D-7" sentinel so live calls surface loudly. Tests
// against the dead-table shape are skipped — see _test.go.
type SeriesPeopleRepository struct {
	db *gorm.DB
}

func NewSeriesPeopleRepository(db *gorm.DB) *SeriesPeopleRepository {
	return &SeriesPeopleRepository{db: db}
}

func (r *SeriesPeopleRepository) Get(ctx context.Context, id int64) (people.SeriesCredit, error) {
	_, _ = ctx, id
	panic("not implemented — pending D-7 i18n+seriesdetail rewrite (D2-revised-roadmap.md); series_people dropped in D-1, replaced by person_credits(media_type='tv_series')")
}

func (r *SeriesPeopleRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID, kind people.SeriesCreditKind) ([]people.SeriesCredit, error) {
	_, _, _ = ctx, seriesID, kind
	panic("not implemented — pending D-7 i18n+seriesdetail rewrite (D2-revised-roadmap.md); series_people dropped in D-1, replaced by person_credits(media_type='tv_series')")
}

func (r *SeriesPeopleRepository) ListByPerson(ctx context.Context, personID int64) ([]people.SeriesCredit, error) {
	_, _ = ctx, personID
	panic("not implemented — pending D-7 i18n+seriesdetail rewrite (D2-revised-roadmap.md); series_people dropped in D-1, replaced by person_credits(media_type='tv_series')")
}

func (r *SeriesPeopleRepository) Upsert(ctx context.Context, c people.SeriesCredit) (int64, error) {
	_, _ = ctx, c
	panic("not implemented — pending D-7 i18n+seriesdetail rewrite (D2-revised-roadmap.md); series_people dropped in D-1, replaced by person_credits(media_type='tv_series')")
}

func (r *SeriesPeopleRepository) BatchUpsert(ctx context.Context, credits []people.SeriesCredit) ([]int64, error) {
	_, _ = ctx, credits
	panic("not implemented — pending D-7 i18n+seriesdetail rewrite (D2-revised-roadmap.md); series_people dropped in D-1, replaced by person_credits(media_type='tv_series')")
}
