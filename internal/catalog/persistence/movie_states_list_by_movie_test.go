package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestMovieStatesRepository_ListActiveByMovieID proves the movie-detail library
// read (Ф6-R-6a): only ACTIVE rows for the given movie id, ordered by
// instance_name ASC, and a movie with no active rows returns an empty slice.
func TestMovieStatesRepository_ListActiveByMovieID(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMovieStatesRepository(db)

			movieID := mustSeedMovie(t, db, 100, "Dune")
			otherID := mustSeedMovie(t, db, 200, "Arrival")

			avail := "released"
			// Two active rows for movieID on two instances (out-of-order insert).
			require.NoError(t, repo.Upsert(ctx, movie.StateEntry{
				InstanceName: "radarr-zeta", RadarrMovieID: 9, MovieID: movieID,
				TitleSlug: "dune", Monitored: true, HasFile: true,
				Availability: &avail, SizeOnDiskBytes: 42, AddedToRadarr: true,
				UpdatedAt: time.Now().UTC(),
			}))
			require.NoError(t, repo.Upsert(ctx, movie.StateEntry{
				InstanceName: "radarr-alpha", RadarrMovieID: 7, MovieID: movieID,
				TitleSlug: "dune", Monitored: false, HasFile: false,
				UpdatedAt: time.Now().UTC(),
			}))
			// A soft-deleted row for movieID must be excluded.
			require.NoError(t, repo.Upsert(ctx, movie.StateEntry{
				InstanceName: "radarr-gone", RadarrMovieID: 5, MovieID: movieID,
				TitleSlug: "dune", UpdatedAt: time.Now().UTC(),
			}))
			require.NoError(t, repo.SoftDelete(ctx, "radarr-gone", 5))
			// A row for a different movie must not leak in.
			require.NoError(t, repo.Upsert(ctx, movie.StateEntry{
				InstanceName: "radarr-alpha", RadarrMovieID: 11, MovieID: otherID,
				TitleSlug: "arrival", UpdatedAt: time.Now().UTC(),
			}))

			got, err := repo.ListActiveByMovieID(ctx, movieID)
			require.NoError(t, err)
			require.Len(t, got, 2)
			// instance_name ASC deterministic.
			assert.Equal(t, "radarr-alpha", string(got[0].InstanceName))
			assert.Equal(t, "radarr-zeta", string(got[1].InstanceName))
			assert.Equal(t, movieID, got[0].MovieID)
			require.NotNil(t, got[1].Availability)
			assert.Equal(t, "released", *got[1].Availability)

			// Movie with no active rows → empty (non-nil-friendly) slice.
			empty, err := repo.ListActiveByMovieID(ctx, mustSeedMovie(t, db, 300, "Sicario"))
			require.NoError(t, err)
			assert.Empty(t, empty)
		})
	}
}
