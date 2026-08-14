//go:build integration

// Story Ф2.5b — base GET /movies/:tmdb_id hero sidebar + trailer read path against a
// real DB. Seeds a movie with origin countries + original language + two production
// companies + a best trailer, drives moviedetail.UseCase.Get, and asserts studio /
// companies (join order) / country / countries / original_language / trailer. Also
// asserts a second movie with NO trailer omits it. SQLite + testcontainers Postgres.
package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func ptr[T any](v T) *T { return &v }

func TestF25bMovieHeroSidebar_ReadPath(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			movieRepo := enrichpersistence.NewMovieRepository(db)
			companiesRepo := enrichpersistence.NewCompaniesRepository(db)
			videosRepo := enrichpersistence.NewMovieVideosRepository(db)

			baseTID := domain.TMDBID(693134)
			lang := ptr("en")
			baseID, err := movieRepo.Upsert(ctx, movie.Canon{
				TMDBID:           &baseTID,
				Hydration:        movie.HydrationFull,
				Title:            "Dune: Part Two",
				OriginalLanguage: lang,
				OriginCountries:  []string{"US", "CA"},
			})
			require.NoError(t, err)

			// Two companies; SetMovie stores position = input order (c2 first).
			c1, err := companiesRepo.Upsert(ctx, taxonomy.ProductionCompany{
				Name: "Warner Bros.", TMDBID: ptr(domain.TMDBID(174)), OriginCountry: ptr("US"),
			})
			require.NoError(t, err)
			c2, err := companiesRepo.Upsert(ctx, taxonomy.ProductionCompany{
				Name: "Legendary Pictures", TMDBID: ptr(domain.TMDBID(923)), OriginCountry: ptr("US"),
			})
			require.NoError(t, err)
			require.NoError(t, companiesRepo.SetMovie(ctx, baseID, []int64{c2, c1}))

			require.NoError(t, videosRepo.ReplaceBestTrailer(ctx, baseID, &movie.Video{
				TMDBVideoID: ptr("vid1"), Name: "Official Trailer",
				Site: ptr("YouTube"), Key: ptr("Way9Dexny3w"), Type: ptr("Trailer"), Official: true,
			}))

			uc := mdapp.New(
				movieRepo,
				enrichpersistence.NewMovieI18nReadRepository(db),
				enrichpersistence.NewMovieCollectionsRepository(db),
				catalogpersistence.NewMovieStatesRepository(db),
			).WithSidebar(companiesRepo, videosRepo)

			d, err := uc.Get(ctx, baseTID, "en")
			require.NoError(t, err)

			// Companies in join order (c2 first).
			require.Len(t, d.Companies, 2)
			assert.Equal(t, "Legendary Pictures", d.Companies[0].Name, "position ASC → c2 first")
			assert.Equal(t, "Warner Bros.", d.Companies[1].Name)

			// Country / countries / language off canon.
			require.NotNil(t, d.Canon.OriginalLanguage)
			assert.Equal(t, "en", *d.Canon.OriginalLanguage)
			assert.Equal(t, []string{"US", "CA"}, d.Canon.OriginCountries)

			// Trailer present.
			require.NotNil(t, d.Trailer)
			require.NotNil(t, d.Trailer.Key)
			assert.Equal(t, "Way9Dexny3w", *d.Trailer.Key)

			// A second movie with NO trailer omits it.
			otherTID := domain.TMDBID(700)
			_, err = movieRepo.Upsert(ctx, movie.Canon{
				TMDBID: &otherTID, Hydration: movie.HydrationFull, Title: "Solo",
			})
			require.NoError(t, err)
			d2, err := uc.Get(ctx, otherTID, "en")
			require.NoError(t, err)
			assert.Nil(t, d2.Trailer, "no trailer row → omitted")
			assert.Empty(t, d2.Companies)
		})
	}
}
