package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

var collNow = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

const collLimit = 50

// seedKeyword inserts a canonical keyword dictionary row with a TMDB id.
func seedKeyword(t *testing.T, db *gorm.DB, id int64, tmdbID int64) {
	t.Helper()
	tid := domain.TMDBID(tmdbID)
	require.NoError(t, db.Create(&database.KeywordModel{
		ID:        id,
		TMDBID:    &tid,
		CreatedAt: collNow,
		UpdatedAt: collNow,
	}).Error)
}

// seedSeriesKeyword wires a series to a keyword (composite-PK join row).
func seedSeriesKeyword(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, keywordID int64) {
	t.Helper()
	require.NoError(t, db.Create(&database.SeriesKeywordModel{
		SeriesID:  seriesID,
		KeywordID: keywordID,
	}).Error)
}

// seedCollSeries inserts a series row with an explicit original_title so the
// title-ordering assertions are deterministic.
func seedCollSeries(t *testing.T, db *gorm.DB, id domain.SeriesID, title string) {
	t.Helper()
	seedListSeries(t, db, id, "", nil) // reuse: series row with NULL status/next_air
	// overwrite title deterministically
	require.NoError(t, db.Model(&database.SeriesModel{}).
		Where("id = ?", id).Update("original_title", title).Error)
}

// wireOwned = series row + series_cache membership + one keyword link.
func wireOwned(t *testing.T, db *gorm.DB, instance string, sonarrID domain.SonarrSeriesID, seriesID domain.SeriesID, title string, keywordID int64) {
	t.Helper()
	seedCollSeries(t, db, seriesID, title)
	seedStatsCache(t, db, instance, sonarrID, seriesID, 0, 0, nil) // series_cache (needs series parent)
	seedSeriesKeyword(t, db, seriesID, keywordID)
}

func TestCollectionsRepository_Collection(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCollectionsRepository(db)
			ctx := context.Background()

			seedSonarrInstance(t, db, "main") // defensive FK parent

			// keyword dictionary: kw 10 → tmdb 818 (books), kw 20 → tmdb 9715 (superhero)
			seedKeyword(t, db, 10, 818)
			seedKeyword(t, db, 20, 9715)

			// Series 1 "Bravo" — matches kw 818.
			wireOwned(t, db, "main", 1, 1, "Bravo", 10)
			// Series 2 "Alpha" — matches kw 818 (alphabetically before Bravo).
			wireOwned(t, db, "main", 2, 2, "Alpha", 10)
			// Series 3 "Charlie" — matches kw 9715 (different collection).
			wireOwned(t, db, "main", 3, 3, "Charlie", 20)

			// books collection = {818}
			res, err := repo.Collection(ctx, "main", []int64{818}, collLimit)
			require.NoError(t, err)
			assert.Equal(t, 2, res.OwnedCount)
			require.Len(t, res.Series, 2)
			// title-ordered: Alpha before Bravo
			assert.Equal(t, "Alpha", res.Series[0].Title)
			assert.Equal(t, domain.SeriesID(2), res.Series[0].SeriesID)
			assert.Equal(t, domain.SonarrSeriesID(2), res.Series[0].SonarrID)
			assert.Equal(t, "Bravo", res.Series[1].Title)

			// superhero collection = {9715}
			res2, err := repo.Collection(ctx, "main", []int64{9715}, collLimit)
			require.NoError(t, err)
			assert.Equal(t, 1, res2.OwnedCount)
			require.Len(t, res2.Series, 1)
			assert.Equal(t, "Charlie", res2.Series[0].Title)
		})
	}
}

// A series matching MULTIPLE ids in the same collection is counted ONCE.
func TestCollectionsRepository_UnionDedup(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCollectionsRepository(db)
			ctx := context.Background()

			seedSonarrInstance(t, db, "main")
			seedKeyword(t, db, 10, 9715) // superhero
			seedKeyword(t, db, 11, 9717) // based on comic

			// Series 1 carries BOTH keywords — union collection {9715,9717}.
			seedCollSeries(t, db, 1, "Watchmen")
			seedStatsCache(t, db, "main", 1, 1, 0, 0, nil)
			seedSeriesKeyword(t, db, 1, 10)
			seedSeriesKeyword(t, db, 1, 11)

			res, err := repo.Collection(ctx, "main", []int64{9715, 9717}, collLimit)
			require.NoError(t, err)
			assert.Equal(t, 1, res.OwnedCount, "a series matching two ids counts once")
			require.Len(t, res.Series, 1)
			assert.Equal(t, "Watchmen", res.Series[0].Title)
		})
	}
}

// deleted_at rows and other-instance rows are excluded.
func TestCollectionsRepository_Scoping(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCollectionsRepository(db)
			ctx := context.Background()

			seedSonarrInstance(t, db, "a")
			seedSonarrInstance(t, db, "b")
			seedKeyword(t, db, 10, 818)

			// instance a, live → counts.
			wireOwned(t, db, "a", 1, 1, "Alpha", 10)
			// instance b, live → must NOT leak into a.
			wireOwned(t, db, "b", 2, 2, "Beta", 10)
			// instance a, soft-deleted → excluded.
			deleted := collNow
			seedCollSeries(t, db, 3, "Gamma")
			seedStatsCache(t, db, "a", 3, 3, 0, 0, &deleted)
			seedSeriesKeyword(t, db, 3, 10)

			res, err := repo.Collection(ctx, "a", []int64{818}, collLimit)
			require.NoError(t, err)
			assert.Equal(t, 1, res.OwnedCount, "only instance a's live row")
			require.Len(t, res.Series, 1)
			assert.Equal(t, "Alpha", res.Series[0].Title)

			names, err := repo.DistinctInstances(ctx)
			require.NoError(t, err)
			assert.Equal(t, []string{"a", "b"}, names)
		})
	}
}

// The series list is capped at `limit` while owned_count stays exact.
func TestCollectionsRepository_SeriesCap(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCollectionsRepository(db)
			ctx := context.Background()

			seedSonarrInstance(t, db, "main")
			seedKeyword(t, db, 10, 818)
			for i := 1; i <= 5; i++ {
				wireOwned(t, db, "main", domain.SonarrSeriesID(i), domain.SeriesID(i),
					string(rune('A'+i-1)), 10)
			}

			res, err := repo.Collection(ctx, "main", []int64{818}, 3) // cap 3
			require.NoError(t, err)
			assert.Equal(t, 5, res.OwnedCount, "exact total ignores the cap")
			assert.Len(t, res.Series, 3, "series slice honours the cap")
		})
	}
}

// Empty DB / empty ids → zero result, no error.
func TestCollectionsRepository_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCollectionsRepository(db)
			ctx := context.Background()

			names, err := repo.DistinctInstances(ctx)
			require.NoError(t, err)
			assert.Empty(t, names)

			res, err := repo.Collection(ctx, "main", []int64{818}, collLimit)
			require.NoError(t, err)
			assert.Equal(t, 0, res.OwnedCount)
			assert.Empty(t, res.Series)

			// empty ids short-circuits
			res2, err := repo.Collection(ctx, "main", nil, collLimit)
			require.NoError(t, err)
			assert.Equal(t, 0, res2.OwnedCount)
			assert.Empty(t, res2.Series)
		})
	}
}
