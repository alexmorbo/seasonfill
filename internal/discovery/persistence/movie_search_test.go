package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedMovie inserts one movies canon row. popularity is written straight
// through (nil → NULL). originalTitle nil → NULL column.
func seedMovie(t *testing.T, db *gorm.DB, id domain.MovieID, tmdbID int, title string, originalTitle *string, popularity *float64) {
	t.Helper()
	tid := domain.TMDBID(tmdbID)
	m := database.MovieModel{
		ID:              id,
		TMDBID:          &tid,
		Hydration:       "full",
		Title:           title,
		OriginalTitle:   originalTitle,
		OriginCountries: datatypes.JSON("[]"),
		Popularity:      popularity,
	}
	require.NoError(t, db.Create(&m).Error)
}

// seedMovieI18n inserts one movie_i18n localized row (title only).
func seedMovieI18n(t *testing.T, db *gorm.DB, id domain.MovieID, lang, title string) {
	t.Helper()
	tt := title
	require.NoError(t, db.Create(&database.MovieI18nModel{
		MovieID:  id,
		Language: lang,
		Title:    &tt,
	}).Error)
}

// ADR-0024 Ф0 S0.2 — a localized (ru-RU) movie_i18n title is matchable and the
// displayed title resolves to the requested language. SQLite LOWER folds ASCII
// only, so the Cyrillic fixture searches an interior all-lowercase fragment.
func TestMovieLocalSearch_LocalizedTitleMatchAndDisplay(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieSearchRepository(db)
			ctx := context.Background()

			original := "The Matrix"
			seedMovie(t, db, 1, 603, "The Matrix", &original, nil)
			seedMovieI18n(t, db, 1, "ru-RU", "Матрица")

			// Match via the ru-RU i18n title (interior lowercase fragment).
			ru, err := repo.LocalSearch(ctx, "атриц", "ru-RU", 20)
			require.NoError(t, err)
			require.Len(t, ru, 1)
			assert.Equal(t, "Матрица", ru[0].Title, "display title resolves to requested ru-RU")
			require.NotNil(t, ru[0].TMDBID)
			assert.EqualValues(t, 603, *ru[0].TMDBID)

			// Match via the canon title; ru-RU request still displays ru title.
			en, err := repo.LocalSearch(ctx, "matrix", "ru-RU", 20)
			require.NoError(t, err)
			require.Len(t, en, 1)
			assert.Equal(t, "Матрица", en[0].Title, "canon match, ru display via COALESCE")
		})
	}
}

// ADR-0024 Ф0 S0.2 — original_title is a match target (additive F-03) and a
// movie with NO i18n row still displays its canon title.
func TestMovieLocalSearch_OriginalTitleMatchAndCanonFallback(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieSearchRepository(db)
			ctx := context.Background()

			// Canon title localized, original title differs. No i18n row.
			original := "Le Fabuleux Destin"
			seedMovie(t, db, 1, 194, "Amelie", &original, nil)

			// Match on original_title.
			byOriginal, err := repo.LocalSearch(ctx, "fabuleux", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, byOriginal, 1)
			assert.Equal(t, "Amelie", byOriginal[0].Title, "no i18n → canon title displayed")

			// Match on canon title.
			byCanon, err := repo.LocalSearch(ctx, "amelie", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, byCanon, 1)
			assert.Equal(t, "Amelie", byCanon[0].Title)
		})
	}
}

// ADR-0024 Ф0 S0.2 — display title prefers requested lang, then en-US.
func TestMovieLocalSearch_EnUSFallbackDisplay(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieSearchRepository(db)
			ctx := context.Background()

			seedMovie(t, db, 1, 27205, "Inception", nil, nil)
			seedMovieI18n(t, db, 1, "en-US", "Inception EN")
			// No ru-RU row → ru-RU request falls back to en-US i18n title.

			items, err := repo.LocalSearch(ctx, "inception", "ru-RU", 20)
			require.NoError(t, err)
			require.Len(t, items, 1)
			assert.Equal(t, "Inception EN", items[0].Title, "ru absent → en-US i18n display")
		})
	}
}

// ADR-0024 Ф0 S0.2 — ranking is popularity DESC NULLS LAST, then id ASC.
func TestMovieLocalSearch_RankingAndLimit(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieSearchRepository(db)
			ctx := context.Background()

			hi := 90.0
			lo := 10.0
			seedMovie(t, db, 1, 1, "Space Low", nil, &lo)
			seedMovie(t, db, 2, 2, "Space High", nil, &hi)
			seedMovie(t, db, 3, 3, "Space Null", nil, nil)

			items, err := repo.LocalSearch(ctx, "space", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, items, 3)
			assert.EqualValues(t, 2, items[0].MovieID, "highest popularity first")
			assert.EqualValues(t, 1, items[1].MovieID)
			assert.EqualValues(t, 3, items[2].MovieID, "NULL popularity ranks last")

			// limit caps the result set.
			capped, err := repo.LocalSearch(ctx, "space", "en-US", 2)
			require.NoError(t, err)
			require.Len(t, capped, 2)
		})
	}
}

// Empty / no-match queries return an empty slice, no error.
func TestMovieLocalSearch_EmptyAndNoMatch(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieSearchRepository(db)
			ctx := context.Background()

			seedMovie(t, db, 1, 1, "Solaris", nil, nil)

			empty, err := repo.LocalSearch(ctx, "", "en-US", 20)
			require.NoError(t, err)
			assert.Empty(t, empty)

			none, err := repo.LocalSearch(ctx, "zzznomatch", "en-US", 20)
			require.NoError(t, err)
			assert.Empty(t, none)
		})
	}
}
