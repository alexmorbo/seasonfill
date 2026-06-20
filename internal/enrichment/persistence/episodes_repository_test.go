package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestEpisodesRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
			require.NoError(t, err)
			repo := NewEpisodesRepository(db)

			id, err := repo.Upsert(ctx, series.CanonEpisode{
				SeriesID:      seriesID,
				SeasonNumber:  1,
				EpisodeNumber: 1,
				AirDate:       new(time.Date(2022, 9, 21, 0, 0, 0, 0, time.UTC)),
			})
			require.NoError(t, err)
			require.NotZero(t, id)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, seriesID, got.SeriesID)
			assert.Equal(t, 1, got.SeasonNumber)
			assert.Equal(t, 1, got.EpisodeNumber)
		})
	}
}

func TestEpisodesRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEpisodesRepository(db)
			_, err := repo.Get(context.Background(), 9999)
			require.Error(t, err)

			var typedErr *sharedErrors.EpisodeNotFoundError
			require.True(t, errors.As(err, &typedErr),
				"Get NotFound must expose typed EpisodeNotFoundError via errors.As")
			assert.Equal(t, domain.EpisodeID(9999), typedErr.ID)
		})
	}
}

func TestEpisodesRepository_BatchUpsert_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
			require.NoError(t, err)
			repo := NewEpisodesRepository(db)

			const n = 500
			episodes := make([]series.CanonEpisode, n)
			for i := range n {
				episodes[i] = series.CanonEpisode{
					SeriesID:      seriesID,
					SeasonNumber:  1,
					EpisodeNumber: i + 1,
				}
			}
			start := time.Now()
			ids, err := repo.BatchUpsert(ctx, episodes)
			require.NoError(t, err)
			require.Len(t, ids, n)
			// Budget covers single-round-trip semantics under `-race`. Reference
			// non-race timing on dev macOS is ~150ms for 500 rows; -race + parallel
			// test load inflates that significantly, and shared GitHub Actions
			// runners add further variance (observed 8-19s). 30s budget catches
			// the regression shape ("N round-trips" would be minutes) while
			// tolerating CI runner jitter under heavy -race load.
			assert.Less(t, time.Since(start), 30*time.Second, "batch upsert must complete in one round-trip for 500 rows")

			// Re-batch with the same payload — every id must round-trip equal.
			ids2, err := repo.BatchUpsert(ctx, episodes)
			require.NoError(t, err)
			require.Equal(t, ids, ids2, "second batch must resolve to the same ids by natural key")
		})
	}
}

func TestEpisodesRepository_ListBySeason(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
			require.NoError(t, err)
			repo := NewEpisodesRepository(db)
			for s := 1; s <= 2; s++ {
				for e := 1; e <= 3; e++ {
					_, err := repo.Upsert(ctx, series.CanonEpisode{
						SeriesID:      seriesID,
						SeasonNumber:  s,
						EpisodeNumber: e,
					})
					require.NoError(t, err)
				}
			}
			rows, err := repo.ListBySeason(ctx, seriesID, 2)
			require.NoError(t, err)
			require.Len(t, rows, 3)
			assert.Equal(t, 1, rows[0].EpisodeNumber)
			assert.Equal(t, 3, rows[2].EpisodeNumber)
		})
	}
}

func TestEpisodesRepository_CountBySeries(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesRepo := NewSeriesRepository(db)
			c1 := sampleCanon("Breaking Bad")
			seriesID1, err := seriesRepo.Upsert(ctx, c1)
			require.NoError(t, err)
			// Second series with distinct TMDB id so the upsert doesn't
			// collapse onto seriesID1 via the partial unique key.
			c2 := sampleCanon("Better Call Saul")
			otherTMDB := domain.TMDBID(999)
			c2.TMDBID = &otherTMDB
			otherTVDB := domain.TVDBID(888)
			c2.TVDBID = &otherTVDB
			otherIMDB := domain.IMDBID("tt0000002")
			c2.IMDBID = &otherIMDB
			seriesID2, err := seriesRepo.Upsert(ctx, c2)
			require.NoError(t, err)
			require.NotEqual(t, seriesID1, seriesID2)
			repo := NewEpisodesRepository(db)
			for e := 1; e <= 5; e++ {
				_, err := repo.Upsert(ctx, series.CanonEpisode{
					SeriesID:      seriesID1,
					SeasonNumber:  1,
					EpisodeNumber: e,
				})
				require.NoError(t, err)
			}
			n1, err := repo.CountBySeries(ctx, seriesID1)
			require.NoError(t, err)
			assert.Equal(t, 5, n1)
			n2, err := repo.CountBySeries(ctx, seriesID2)
			require.NoError(t, err)
			assert.Equal(t, 0, n2)
		})
	}
}
