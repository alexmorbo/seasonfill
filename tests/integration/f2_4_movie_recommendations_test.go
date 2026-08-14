//go:build integration

// Story Ф2.4 — GET /movies/:tmdb_id/recommendations read path against a real DB.
// Seeds a base movie + three recommendation stub movies (UpsertStub) and wires
// MovieRecommendationsRepository.Set, then drives RecommendationsUseCase asserting
// TMDB-rank order, per-item tmdb_id/title/poster, and pagination. Runs on
// SQLite + testcontainers Postgres via testhelpers.AllBackends.
package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestF24MovieRecommendations_ReadPath(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			movieRepo := enrichpersistence.NewMovieRepository(db)
			recsRepo := enrichpersistence.NewMovieRecommendationsRepository(db)

			// Base movie (the one whose recs we fetch).
			baseTID := domain.TMDBID(603)
			baseID, err := movieRepo.Upsert(ctx, movie.Canon{
				TMDBID: &baseTID, Hydration: movie.HydrationFull, Title: "The Matrix",
			})
			require.NoError(t, err)

			// Three recommendation stub movies via the real stub writer.
			t604, t605, t606 := domain.TMDBID(604), domain.TMDBID(605), domain.TMDBID(606)
			p604, p605, p606 := "/p604.jpg", "/p605.jpg", "/p606.jpg"
			year04, year05, year06 := 2003, 2003, 2021
			id604, err := movieRepo.UpsertStub(ctx, movie.Canon{
				TMDBID: &t604, Title: "The Matrix Reloaded", PosterAsset: &p604, Year: &year04,
			})
			require.NoError(t, err)
			id605, err := movieRepo.UpsertStub(ctx, movie.Canon{
				TMDBID: &t605, Title: "The Matrix Revolutions", PosterAsset: &p605, Year: &year05,
			})
			require.NoError(t, err)
			id606, err := movieRepo.UpsertStub(ctx, movie.Canon{
				TMDBID: &t606, Title: "The Matrix Resurrections", PosterAsset: &p606, Year: &year06,
			})
			require.NoError(t, err)

			// Rank order 606, 604, 605 (deliberately not id order) — Set stores
			// position = input index.
			require.NoError(t, recsRepo.Set(ctx, baseID, []domain.MovieID{id606, id604, id605}))

			uc := mdapp.NewRecommendationsUseCase(movieRepo, recsRepo, movieRepo)
			page, err := uc.Get(ctx, baseTID, 20, 0)
			require.NoError(t, err)
			require.Len(t, page.Items, 3)
			assert.Equal(t, 3, page.TotalCount)
			assert.False(t, page.HasMore)
			assert.Empty(t, page.Degraded)

			// Rank order preserved: 606, 604, 605.
			require.NotNil(t, page.Items[0].Canon.TMDBID)
			assert.Equal(t, domain.TMDBID(606), *page.Items[0].Canon.TMDBID)
			assert.Equal(t, domain.TMDBID(604), *page.Items[1].Canon.TMDBID)
			assert.Equal(t, domain.TMDBID(605), *page.Items[2].Canon.TMDBID)

			// Per-item title + poster round-trip.
			assert.Equal(t, "The Matrix Resurrections", page.Items[0].Title)
			require.NotNil(t, page.Items[0].Canon.PosterAsset)
			assert.Equal(t, "/p606.jpg", *page.Items[0].Canon.PosterAsset)
			require.NotNil(t, page.Items[1].Canon.Year)
			assert.Equal(t, 2003, *page.Items[1].Canon.Year)

			// Pagination window.
			pageLim, err := uc.Get(ctx, baseTID, 2, 0)
			require.NoError(t, err)
			assert.Len(t, pageLim.Items, 2)
			assert.True(t, pageLim.HasMore)

			// Base movie with no recs → empty, not error.
			otherTID := domain.TMDBID(700)
			_, err = movieRepo.Upsert(ctx, movie.Canon{TMDBID: &otherTID, Hydration: movie.HydrationFull, Title: "Solo"})
			require.NoError(t, err)
			pageEmpty, err := uc.Get(ctx, otherTID, 20, 0)
			require.NoError(t, err)
			assert.Empty(t, pageEmpty.Items)
			assert.Equal(t, 0, pageEmpty.TotalCount)
		})
	}
}
