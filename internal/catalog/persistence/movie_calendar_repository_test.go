package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestMovieCalendarRepository_Events proves the Ф6-R-6a movie release calendar
// query: an in-library movie with all three release dates fans to three rows
// (theatrical/digital/physical); a movie NOT in any library is excluded (library
// scope); and out-of-window dates are filtered.
func TestMovieCalendarRepository_Events(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := enrichpersistence.NewMovieRepository(db)
			states := NewMovieStatesRepository(db)
			repo := NewMovieCalendarRepository(db)

			base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
			// In-library movie with all three milestones inside the window.
			inLibID, err := movies.Upsert(ctx, movie.Canon{
				TMDBID: tmdbIDPtr(603), Hydration: movie.HydrationStub, Title: "The Matrix",
				ReleaseDate:         new(base),
				DigitalReleaseDate:  new(base.AddDate(0, 0, 5)),
				PhysicalReleaseDate: new(base.AddDate(0, 0, 10)),
			})
			require.NoError(t, err)
			require.NoError(t, states.Upsert(ctx, movie.StateEntry{
				InstanceName: "main", RadarrMovieID: 1, MovieID: inLibID,
				TitleSlug: "the-matrix", UpdatedAt: time.Now().UTC(),
			}))

			// A movie NOT in any library — must be excluded by the EXISTS clause.
			_, err = movies.Upsert(ctx, movie.Canon{
				TMDBID: tmdbIDPtr(604), Hydration: movie.HydrationStub, Title: "Reloaded",
				ReleaseDate: new(base.AddDate(0, 0, 2)),
			})
			require.NoError(t, err)

			// An in-library movie whose only date is OUT of window.
			outID, err := movies.Upsert(ctx, movie.Canon{
				TMDBID: tmdbIDPtr(605), Hydration: movie.HydrationStub, Title: "Revolutions",
				ReleaseDate: new(base.AddDate(1, 0, 0)),
			})
			require.NoError(t, err)
			require.NoError(t, states.Upsert(ctx, movie.StateEntry{
				InstanceName: "main", RadarrMovieID: 2, MovieID: outID,
				TitleSlug: "revolutions", UpdatedAt: time.Now().UTC(),
			}))

			from := base.AddDate(0, 0, -1)
			to := base.AddDate(0, 0, 30)
			rows, err := repo.Events(ctx, from, to)
			require.NoError(t, err)

			// Exactly the three milestones of the in-library, in-window movie.
			require.Len(t, rows, 3)
			milestones := map[string]bool{}
			for _, r := range rows {
				assert.Equal(t, int64(inLibID), r.MovieID)
				milestones[r.Milestone] = true
			}
			assert.True(t, milestones["theatrical"])
			assert.True(t, milestones["digital"])
			assert.True(t, milestones["physical"])
		})
	}
}
