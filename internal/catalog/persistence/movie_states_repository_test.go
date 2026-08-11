package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestMovieStatesRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movieID := mustSeedMovie(t, db, 100, "Dune")
			repo := NewMovieStatesRepository(db)

			avail := "released"
			require.NoError(t, repo.Upsert(ctx, movie.StateEntry{
				InstanceName: "radarr-main", RadarrMovieID: 7, MovieID: movieID,
				TitleSlug: "dune-2021", Monitored: true, HasFile: true,
				Availability: &avail, SizeOnDiskBytes: 5_000_000_000, AddedToRadarr: true,
				UpdatedAt: time.Now().UTC(),
			}))

			got, err := repo.Get(ctx, "radarr-main", 7)
			require.NoError(t, err)
			assert.Equal(t, movieID, got.MovieID)
			assert.True(t, got.HasFile)
			require.NotNil(t, got.Availability)
			assert.Equal(t, "released", *got.Availability)
			assert.Equal(t, int64(5_000_000_000), got.SizeOnDiskBytes)
			assert.True(t, got.IsActive())
		})
	}
}

// TestMovieStatesRepository_StubPreservesStats — the thin webhook writer
// (UpsertStub) MUST NOT zero size_on_disk_bytes / availability written by the
// rich sync writer. Mirror of the series_cache stub-preservation guarantee.
func TestMovieStatesRepository_StubPreservesStats(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movieID := mustSeedMovie(t, db, 200, "Arrival")
			repo := NewMovieStatesRepository(db)
			avail := "released"

			require.NoError(t, repo.Upsert(ctx, movie.StateEntry{
				InstanceName: "r1", RadarrMovieID: 9, MovieID: movieID, TitleSlug: "arrival",
				Monitored: true, HasFile: true, Availability: &avail,
				SizeOnDiskBytes: 4_000_000_000, AddedToRadarr: true, UpdatedAt: time.Now().UTC(),
			}))
			// Thin webhook write: no availability, no size — must NOT blank them.
			require.NoError(t, repo.UpsertStub(ctx, movie.StateEntry{
				InstanceName: "r1", RadarrMovieID: 9, MovieID: movieID, TitleSlug: "arrival",
				Monitored: true, HasFile: false, AddedToRadarr: true, UpdatedAt: time.Now().UTC(),
			}))

			got, err := repo.Get(ctx, "r1", 9)
			require.NoError(t, err)
			require.NotNil(t, got.Availability, "availability preserved by stub")
			assert.Equal(t, "released", *got.Availability)
			assert.Equal(t, int64(4_000_000_000), got.SizeOnDiskBytes, "size preserved by stub")
			assert.False(t, got.HasFile, "has_file updated by stub")
		})
	}
}

func TestMovieStatesRepository_SoftDeleteAndResurrect(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movieID := mustSeedMovie(t, db, 300, "Sicario")
			repo := NewMovieStatesRepository(db)
			base := movie.StateEntry{InstanceName: "r1", RadarrMovieID: 5, MovieID: movieID,
				TitleSlug: "sicario", Monitored: true, AddedToRadarr: true, UpdatedAt: time.Now().UTC()}
			require.NoError(t, repo.Upsert(ctx, base))

			require.NoError(t, repo.SoftDelete(ctx, "r1", 5))
			got, err := repo.Get(ctx, "r1", 5)
			require.NoError(t, err)
			assert.False(t, got.IsActive(), "soft-deleted")

			// Idempotent second delete → ErrNotFound (no active row).
			err = repo.SoftDelete(ctx, "r1", 5)
			assert.True(t, errors.Is(err, ports.ErrNotFound))

			// A rich Upsert resurrects (deleted_at -> NULL).
			require.NoError(t, repo.Upsert(ctx, base))
			got, err = repo.Get(ctx, "r1", 5)
			require.NoError(t, err)
			assert.True(t, got.IsActive(), "resurrected")

			active, err := repo.ListActiveByInstance(ctx, "r1")
			require.NoError(t, err)
			assert.Len(t, active, 1)
		})
	}
}

func TestMovieStatesRepository_NullAndErrorPairs(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMovieStatesRepository(db)
			// error pairs: empty instance / zero radarr id / zero movie id.
			require.Error(t, repo.Upsert(ctx, movie.StateEntry{RadarrMovieID: 1, MovieID: 1}))
			require.Error(t, repo.Upsert(ctx, movie.StateEntry{InstanceName: "r1", MovieID: 1}))
			require.Error(t, repo.Upsert(ctx, movie.StateEntry{InstanceName: "r1", RadarrMovieID: 1}))
			// Get miss → ErrNotFound.
			_, err := repo.Get(ctx, "r1", 999)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

// mustSeedMovie inserts a movies canon row (FK target for movie_states) and
// returns its id.
func mustSeedMovie(t *testing.T, db *gorm.DB, tmdbID int, title string) domain.MovieID {
	t.Helper()
	id, err := enrichpersistence.NewMovieRepository(db).Upsert(context.Background(), movie.Canon{
		TMDBID: tmdbIDPtr(tmdbID), Hydration: movie.HydrationStub, Title: title,
	})
	require.NoError(t, err)
	return id
}

func tmdbIDPtr(v int) *domain.TMDBID {
	id := domain.TMDBID(v)
	return &id
}
