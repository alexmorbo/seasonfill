package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestMovieI18nReadRepository_Get proves the localized read added for the
// movie-detail aggregate (Ф6-R-6a): a seeded (movie_id, lang) row returns its
// localized fields; a missing row returns ports.ErrNotFound (never an error the
// caller can't classify); a wrong language misses the same way.
func TestMovieI18nReadRepository_Get(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			tid := domain.TMDBID(693134)
			movieID, err := NewMovieRepository(db).Upsert(ctx, movie.Canon{
				TMDBID:    &tid,
				Hydration: movie.HydrationStub,
				Title:     "Dune: Part Two",
			})
			require.NoError(t, err)
			require.NotZero(t, movieID)

			poster := "/ru-poster.jpg"
			require.NoError(t, NewMovieI18nSeeder(db).SeedStub(ctx, movieID, "ru-RU", "Дюна: Часть вторая", &poster, nil))

			read := NewMovieI18nReadRepository(db)

			// Hit: localized fields resolved.
			row, err := read.Get(ctx, movieID, "ru-RU")
			require.NoError(t, err)
			require.NotNil(t, row.Title)
			assert.Equal(t, "Дюна: Часть вторая", *row.Title)
			require.NotNil(t, row.Poster)
			assert.Equal(t, "/ru-poster.jpg", *row.Poster)

			// Miss: language not seeded → ports.ErrNotFound.
			_, err = read.Get(ctx, movieID, "en-US")
			require.ErrorIs(t, err, ports.ErrNotFound)

			// Miss: unknown movie id → ports.ErrNotFound.
			_, err = read.Get(ctx, domain.MovieID(999999), "ru-RU")
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}
