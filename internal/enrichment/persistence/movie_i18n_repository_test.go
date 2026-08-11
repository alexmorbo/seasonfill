package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestMovieI18nSeeder_SeedsRequestLangOnly is the #1184 guard for the movie
// vertical: a discovery stub seeds movie_i18n under the REQUEST lang only —
// never the base en-US — and never overwrites an already-present localized
// row (DoNothing on conflict).
func TestMovieI18nSeeder_SeedsRequestLangOnly(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			movieRepo := NewMovieRepository(db)
			id, err := movieRepo.Upsert(ctx, movie.Canon{
				TMDBID:    new(domain.TMDBID(693134)),
				Hydration: movie.HydrationStub,
				Title:     "Dune: Part Two",
			})
			require.NoError(t, err)
			require.NotZero(t, id)

			seeder := NewMovieI18nSeeder(db)
			poster := "/p.jpg"
			require.NoError(t, seeder.SeedStub(ctx, id, "ru-RU", "Дюна: Часть вторая", &poster, nil))

			// ru-RU row seeded under the request lang.
			var ru database.MovieI18nModel
			require.NoError(t, db.WithContext(ctx).
				Where("movie_id = ? AND language = ?", id, "ru-RU").First(&ru).Error)
			require.NotNil(t, ru.Title)
			assert.Equal(t, "Дюна: Часть вторая", *ru.Title)

			// base en-US must NOT have been written by the stub path (#1184).
			var count int64
			require.NoError(t, db.WithContext(ctx).Model(&database.MovieI18nModel{}).
				Where("movie_id = ? AND language = ?", id, "en-US").Count(&count).Error)
			assert.Zero(t, count, "stub must not seed the base en-US row")

			// Only-if-absent: a second seed with a different title must NOT
			// overwrite the existing ru-RU title (enrichment owns the row).
			require.NoError(t, seeder.SeedStub(ctx, id, "ru-RU", "WRONG", nil, nil))
			var ru2 database.MovieI18nModel
			require.NoError(t, db.WithContext(ctx).
				Where("movie_id = ? AND language = ?", id, "ru-RU").First(&ru2).Error)
			require.NotNil(t, ru2.Title)
			assert.Equal(t, "Дюна: Часть вторая", *ru2.Title, "DoNothing must preserve the first-seeded title")
		})
	}
}

// TestMovieI18nSeeder_Guards covers the zero-id + empty-lang error paths.
func TestMovieI18nSeeder_Guards(t *testing.T) {
	t.Parallel()
	db := testhelpers.AllBackends(t)[0].NewDB(t)
	seeder := NewMovieI18nSeeder(db)
	require.Error(t, seeder.SeedStub(context.Background(), 0, "ru-RU", "x", nil, nil))
	require.Error(t, seeder.SeedStub(context.Background(), 1, "", "x", nil, nil))
}
