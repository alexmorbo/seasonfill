package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedSearchable inserts one series row plus a matching en-US series_texts
// row so the LocalSearch WHERE EXISTS title probe finds it. rating is
// written straight to series.tmdb_rating (nil → NULL column). Returns the
// unique title token used so the caller can query for it.
func seedSearchable(t *testing.T, db *gorm.DB, rating *float64) string {
	t.Helper()
	token := "SearchRating" + uuid.NewString()[:8]
	m := database.SeriesModel{
		OriginalTitle:   &token,
		Hydration:       "stub",
		InProduction:    false,
		OriginCountries: datatypes.JSON("[]"),
		TMDBRating:      rating,
	}
	require.NoError(t, db.Create(&m).Error)
	require.NotZero(t, m.ID)

	title := token
	require.NoError(t, db.Create(&database.SeriesTextModel{
		SeriesID: m.ID,
		Language: "en-US",
		Title:    &title,
	}).Error)
	return token
}

// W18-2 — LocalSearch must pass series.tmdb_rating through to
// disco.Item.TMDBRating so search cards render the ★ badge like
// trending/popular/genre lists do. A non-null rating surfaces as a
// non-nil pointer with the equal value.
func TestLocalSearch_TMDBRating_NonNull_PassesThrough_W18_2(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSearchRepository(db)
			ctx := context.Background()

			rating := 8.4
			token := seedSearchable(t, db, &rating)

			items, err := repo.LocalSearch(ctx, token, "en-US", 20)
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.NotNil(t, items[0].TMDBRating,
				"non-null series.tmdb_rating must surface as a non-nil Item.TMDBRating")
			assert.InDelta(t, rating, *items[0].TMDBRating, 1e-9)
		})
	}
}

// W18-2 — a NULL series.tmdb_rating must map to a nil Item.TMDBRating
// (no zero-value ★) rather than a 0.0 pointer.
func TestLocalSearch_TMDBRating_Null_StaysNil_W18_2(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSearchRepository(db)
			ctx := context.Background()

			token := seedSearchable(t, db, nil)

			items, err := repo.LocalSearch(ctx, token, "en-US", 20)
			require.NoError(t, err)
			require.Len(t, items, 1)
			assert.Nil(t, items[0].TMDBRating,
				"NULL series.tmdb_rating must stay nil, not a 0.0 pointer")
		})
	}
}
