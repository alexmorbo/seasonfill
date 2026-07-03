package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	dbmodels "github.com/alexmorbo/seasonfill/internal/shared/db"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestTopGenres exercises the TopKindsReader against both the SQLite
// in-memory shadow and (when SEASONFILL_TEST_POSTGRES_ENABLE=1) the
// real Postgres testcontainer per testhelpers.AllBackends — matching
// the D-0 test quality bar (project_seasonfill_test_quality_bar).
func TestTopGenres(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := persistence.NewTopKindsReader(db)
			ctx := context.Background()

			t.Run("empty catalog returns empty slice", func(t *testing.T) {
				got, err := r.TopGenres(ctx, 10)
				require.NoError(t, err)
				require.Equal(t, []int{}, got)
			})

			t.Run("limit zero returns empty without query", func(t *testing.T) {
				got, err := r.TopGenres(ctx, 0)
				require.NoError(t, err)
				require.Equal(t, []int{}, got)
			})

			t.Run("top 2 by occurrence", func(t *testing.T) {
				seedSeriesWithGenres(t, db, [][]int{
					{18, 35}, // Drama, Comedy
					{18, 80}, // Drama, Crime
					{18, 35}, // Drama, Comedy
				})

				got, err := r.TopGenres(ctx, 2)
				require.NoError(t, err)
				// Drama=3, Comedy=2, Crime=1 → top 2 = [18, 35]
				require.Equal(t, []int{18, 35}, got)
			})

			t.Run("limit caps result count", func(t *testing.T) {
				got, err := r.TopGenres(ctx, 1)
				require.NoError(t, err)
				require.Len(t, got, 1)
			})

			t.Run("null tmdb_id genres are filtered", func(t *testing.T) {
				seedFallbackGenreSeries(t, db)
				got, err := r.TopGenres(ctx, 10)
				require.NoError(t, err)
				for _, id := range got {
					require.NotZero(t, id, "tmdb_id 0/NULL must not surface")
				}
			})
		})
	}
}

func TestTopNetworks(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := persistence.NewTopKindsReader(db)
			ctx := context.Background()

			t.Run("empty catalog returns empty slice", func(t *testing.T) {
				got, err := r.TopNetworks(ctx, 10)
				require.NoError(t, err)
				require.Equal(t, []int{}, got)
			})

			t.Run("top 2 by occurrence", func(t *testing.T) {
				seedSeriesWithNetworks(t, db, [][]int{
					{213, 49}, // Netflix, HBO
					{213, 88}, // Netflix, AdultSwim
					{213},     // Netflix
				})

				got, err := r.TopNetworks(ctx, 2)
				require.NoError(t, err)
				require.Equal(t, []int{213, 49}, got)
			})
		})
	}
}

// seedSeriesWithGenres inserts one SeriesModel + GenreModel(per id) +
// SeriesGenreModel rows per row of `genreSets`. UUID-derived title
// isolates concurrent test data.
func seedSeriesWithGenres(t *testing.T, db *gorm.DB, genreSets [][]int) {
	t.Helper()
	ctx := context.Background()
	for _, set := range genreSets {
		seriesID := insertSeries(t, db)
		for _, tmdbGenreID := range set {
			genreID := upsertGenre(t, db, tmdbGenreID)
			require.NoError(t, db.WithContext(ctx).Create(&dbmodels.SeriesGenreModel{
				SeriesID: seriesID,
				GenreID:  genreID,
			}).Error)
		}
	}
}

func seedSeriesWithNetworks(t *testing.T, db *gorm.DB, networkSets [][]int) {
	t.Helper()
	ctx := context.Background()
	for _, set := range networkSets {
		seriesID := insertSeries(t, db)
		for _, tmdbNetID := range set {
			networkID := upsertNetwork(t, db, tmdbNetID)
			require.NoError(t, db.WithContext(ctx).Create(&dbmodels.SeriesNetworkModel{
				SeriesID:  seriesID,
				NetworkID: networkID,
			}).Error)
		}
	}
}

// seedFallbackGenreSeries inserts a genre with tmdb_id=NULL (the
// PRD §5.4 Sonarr-string fallback path) and attaches it to a fresh
// series — the row MUST NOT surface in TopGenres output.
func seedFallbackGenreSeries(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	seriesID := insertSeries(t, db)
	g := dbmodels.GenreModel{ /* TMDBID intentionally nil */ }
	require.NoError(t, db.WithContext(ctx).Create(&g).Error)
	require.NoError(t, db.WithContext(ctx).Create(&dbmodels.SeriesGenreModel{
		SeriesID: seriesID,
		GenreID:  g.ID,
	}).Error)
}

func insertSeries(t *testing.T, db *gorm.DB) shareddomain.SeriesID {
	t.Helper()
	ctx := context.Background()
	title := "tst-" + uuid.NewString()[:8]
	s := dbmodels.SeriesModel{
		OriginalTitle:   &title,
		Hydration:       "stub",
		OriginCountries: datatypes.JSON("[]"),
	}
	require.NoError(t, db.WithContext(ctx).Create(&s).Error)
	return s.ID
}

func upsertGenre(t *testing.T, db *gorm.DB, tmdbID int) int64 {
	t.Helper()
	ctx := context.Background()
	tid := shareddomain.TMDBID(tmdbID)
	var g dbmodels.GenreModel
	err := db.WithContext(ctx).Where("tmdb_id = ?", tid).Take(&g).Error
	if err == nil {
		return g.ID
	}
	g = dbmodels.GenreModel{TMDBID: &tid}
	require.NoError(t, db.WithContext(ctx).Create(&g).Error)
	return g.ID
}

func upsertNetwork(t *testing.T, db *gorm.DB, tmdbID int) int64 {
	t.Helper()
	ctx := context.Background()
	tid := shareddomain.TMDBID(tmdbID)
	var n dbmodels.NetworkModel
	err := db.WithContext(ctx).Where("tmdb_id = ?", tid).Take(&n).Error
	if err == nil {
		return n.ID
	}
	n = dbmodels.NetworkModel{TMDBID: &tid, Name: "n-" + uuid.NewString()[:8]}
	require.NoError(t, db.WithContext(ctx).Create(&n).Error)
	return n.ID
}
