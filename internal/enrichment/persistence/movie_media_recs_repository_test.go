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

func TestMovieVideosRepository_ReplaceBestTrailer(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := NewMovieRepository(db)
			videos := NewMovieVideosRepository(db)

			movieID := mkMovie(t, movies, 603, "The Matrix")

			require.NoError(t, videos.ReplaceBestTrailer(ctx, movieID, &movie.Video{
				TMDBVideoID: new("vid1"), Name: "Official Trailer",
				Site: new("YouTube"), Key: new("abc"), Type: new("Trailer"), Official: true,
			}))

			var cnt int64
			require.NoError(t, db.Table("movie_videos").Where("movie_id = ?", movieID).Count(&cnt).Error)
			assert.EqualValues(t, 1, cnt)

			// replace: a new best trailer removes the prior row (authoritative single-row).
			require.NoError(t, videos.ReplaceBestTrailer(ctx, movieID, &movie.Video{
				TMDBVideoID: new("vid2"), Name: "Final Trailer",
				Site: new("YouTube"), Key: new("def"), Type: new("Trailer"), Official: true,
			}))
			var keys []string
			require.NoError(t, db.Table("movie_videos").Where("movie_id = ?", movieID).Pluck("key", &keys).Error)
			require.Len(t, keys, 1)
			assert.Equal(t, "def", keys[0])

			// nil clears.
			require.NoError(t, videos.ReplaceBestTrailer(ctx, movieID, nil))
			require.NoError(t, db.Table("movie_videos").Where("movie_id = ?", movieID).Count(&cnt).Error)
			assert.EqualValues(t, 0, cnt)

			// zero movie_id rejected.
			require.Error(t, videos.ReplaceBestTrailer(ctx, 0, nil))
		})
	}
}

func TestMovieRecommendationsRepository_SetReplacesAndOrders(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := NewMovieRepository(db)
			recs := NewMovieRecommendationsRepository(db)

			parent := mkMovie(t, movies, 603, "The Matrix")
			r1 := mkMovie(t, movies, 604, "Reloaded")
			r2 := mkMovie(t, movies, 605, "Revolutions")

			require.NoError(t, recs.Set(ctx, parent, []domain.MovieID{r1, r2}))
			got, err := recs.ListByMovie(ctx, parent)
			require.NoError(t, err)
			assert.Equal(t, []domain.MovieID{r1, r2}, got, "position-ordered")

			// replace authoritative.
			require.NoError(t, recs.Set(ctx, parent, []domain.MovieID{r2}))
			got, err = recs.ListByMovie(ctx, parent)
			require.NoError(t, err)
			assert.Equal(t, []domain.MovieID{r2}, got)

			// self-ref rejected.
			require.Error(t, recs.Set(ctx, parent, []domain.MovieID{parent}))

			// empty clears.
			require.NoError(t, recs.Set(ctx, parent, nil))
			got, err = recs.ListByMovie(ctx, parent)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

// TestMovieRecommendations_ColdRecs_NoFKViolation is the F-Ф1-12 critical path. It reproduces
// the writer's persistence sequence (UpsertStub each rec → Set the join) against a cold DB
// where the recommended movies do NOT pre-exist. On the Postgres backend (opt-in via
// SEASONFILL_TEST_POSTGRES_ENABLE / `make test-integration-postgres`) both movie_recommendations
// FKs → movies(id) are REALLY enforced, so a join insert before the stub upserts would raise
// SQLSTATE 23503 — this test proves the stub-before-join ordering prevents it. It also asserts
// an already-'full' recommended movie is NOT downgraded to 'stub' by UpsertStub.
func TestMovieRecommendations_ColdRecs_NoFKViolation(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := NewMovieRepository(db)
			recs := NewMovieRecommendationsRepository(db)

			parent := mkMovie(t, movies, 603, "The Matrix")

			// Pre-existing FULL recommended movie (must NOT be downgraded by the stub upsert).
			fullID, err := movies.Upsert(ctx, movie.Canon{
				TMDBID: ptrTMDBID(604), Hydration: movie.HydrationFull, Title: "Reloaded (full)",
			})
			require.NoError(t, err)

			// Cold recs: 604 already full; 605 + 606 do NOT exist yet. Simulate the writer:
			// UpsertStub each recommended movie FIRST, collecting ids in TMDB-rank order.
			recStubs := []movie.Canon{
				{TMDBID: ptrTMDBID(604), Hydration: movie.HydrationStub, Title: "Reloaded (stub)"},
				{TMDBID: ptrTMDBID(605), Hydration: movie.HydrationStub, Title: "Revolutions"},
				{TMDBID: ptrTMDBID(606), Hydration: movie.HydrationStub, Title: "Resurrections"},
			}
			recIDs := make([]domain.MovieID, 0, len(recStubs))
			for _, s := range recStubs {
				id, uerr := movies.UpsertStub(ctx, s)
				require.NoError(t, uerr)
				require.NotZero(t, id)
				recIDs = append(recIDs, id)
			}

			// (a) stub movies rows were created for the previously-absent recs.
			got605, err := movies.GetByTMDBID(ctx, domain.TMDBID(605))
			require.NoError(t, err)
			assert.Equal(t, movie.HydrationStub, got605.Hydration)
			got606, err := movies.GetByTMDBID(ctx, domain.TMDBID(606))
			require.NoError(t, err)
			assert.Equal(t, movie.HydrationStub, got606.Hydration)

			// (c) the already-'full' movie 604 was NOT downgraded, and its title preserved
			// (existing-wins COALESCE polarity).
			got604, err := movies.GetByTMDBID(ctx, domain.TMDBID(604))
			require.NoError(t, err)
			assert.Equal(t, movie.HydrationFull, got604.Hydration, "full must never downgrade to stub")
			assert.Equal(t, "Reloaded (full)", got604.Title, "existing title preserved")
			assert.Equal(t, fullID, got604.ID)

			// (b) join persisted with NO FK violation now that every recommended_movie_id exists.
			require.NoError(t, recs.Set(ctx, parent, recIDs))
			stored, err := recs.ListByMovie(ctx, parent)
			require.NoError(t, err)
			assert.Equal(t, recIDs, stored)
		})
	}
}

func TestMovieVideosRepository_GetBestTrailer(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := NewMovieRepository(db)
			videos := NewMovieVideosRepository(db)

			movieID := mkMovie(t, movies, 603, "The Matrix")

			// No trailer yet → ErrNotFound.
			_, err := videos.GetBestTrailer(ctx, movieID)
			require.ErrorIs(t, err, ports.ErrNotFound)

			require.NoError(t, videos.ReplaceBestTrailer(ctx, movieID, &movie.Video{
				TMDBVideoID: new("vid1"), Name: "Official Trailer",
				Site: new("YouTube"), Key: new("abc"), Type: new("Trailer"), Official: true,
			}))

			got, err := videos.GetBestTrailer(ctx, movieID)
			require.NoError(t, err)
			assert.Equal(t, "Official Trailer", got.Name)
			require.NotNil(t, got.Key)
			assert.Equal(t, "abc", *got.Key)
			assert.True(t, got.Official)

			// nil clears → ErrNotFound again.
			require.NoError(t, videos.ReplaceBestTrailer(ctx, movieID, nil))
			_, err = videos.GetBestTrailer(ctx, movieID)
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}
