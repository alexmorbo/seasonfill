//go:build integration

// Story Ф2.3 — GET /movies/:tmdb_id/ratings read path against a real DB.
// Seeds a movie's TMDB ratings (Upsert) + OMDb-owned columns (the real
// UpdateMovieOMDbColumns writer) and asserts the RatingsUseCase round-trips all
// six fields. Runs on SQLite + testcontainers Postgres via testhelpers.AllBackends.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestF23MovieRatings_ReadPath(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			now := time.Now().UTC()
			tid := domain.TMDBID(603)

			movieRepo := enrichpersistence.NewMovieRepository(db)
			movieID, err := movieRepo.Upsert(ctx, movie.Canon{
				TMDBID: &tid, Hydration: movie.HydrationFull, Title: "The Matrix",
				TMDBRating: new(8.2), TMDBVotes: new(24000),
			})
			require.NoError(t, err)

			// OMDb-owned columns via the real production writer (sole owner of the
			// four OMDb columns).
			require.NoError(t, movieRepo.UpdateMovieOMDbColumns(ctx, movieID, omdb.Enrichment{
				IMDBRating: new(8.7),
				IMDBVotes:  new(int64(1900000)),
				OMDbRated:  new("R"),
				OMDbAwards: new("Won 4 Oscars."),
			}, now))

			uc := mdapp.NewRatingsUseCase(movieRepo)
			page, err := uc.Get(ctx, tid)
			require.NoError(t, err)
			require.NotNil(t, page.TMDBRating)
			assert.InDelta(t, 8.2, *page.TMDBRating, 1e-9)
			require.NotNil(t, page.TMDBVotes)
			assert.Equal(t, 24000, *page.TMDBVotes)
			require.NotNil(t, page.IMDBRating)
			assert.InDelta(t, 8.7, *page.IMDBRating, 1e-9)
			require.NotNil(t, page.IMDBVotes)
			assert.Equal(t, 1900000, *page.IMDBVotes)
			require.NotNil(t, page.Rated)
			assert.Equal(t, "R", *page.Rated)
			require.NotNil(t, page.Awards)
			assert.Equal(t, "Won 4 Oscars.", *page.Awards)
		})
	}
}
