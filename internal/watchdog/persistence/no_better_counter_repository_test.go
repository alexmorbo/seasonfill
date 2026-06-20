package persistence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestNoBetterCounterRepository_Increment_FreshTriple(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
			got, err := repo.Increment(ctx, 7, 122, 2, now)
			require.NoError(t, err)
			assert.Equal(t, uint(7), got.InstanceID)
			assert.Equal(t, domain.SonarrSeriesID(122), got.SeriesID)
			assert.Equal(t, 2, got.SeasonNumber)
			assert.Equal(t, 1, got.Consecutive)
			assert.True(t, got.CreatedAt.Equal(now))
			assert.True(t, got.UpdatedAt.Equal(now))
			assert.True(t, got.LastSeenAt.Equal(now))
		})
	}
}

func TestNoBetterCounterRepository_Increment_ExistingTriple(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			ctx := context.Background()

			first := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
			_, err := repo.Increment(ctx, 7, 122, 2, first)
			require.NoError(t, err)

			second := first.Add(time.Hour)
			got, err := repo.Increment(ctx, 7, 122, 2, second)
			require.NoError(t, err)
			assert.Equal(t, 2, got.Consecutive)
			assert.True(t, got.UpdatedAt.Equal(second))
			assert.True(t, got.LastSeenAt.Equal(second))
			// CreatedAt is preserved from first call.
			assert.True(t, got.CreatedAt.Equal(first))
		})
	}
}

func TestNoBetterCounterRepository_Increment_RejectsInvalidTriple(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			now := time.Now().UTC()

			cases := []struct {
				name     string
				instance uint
				series   domain.SonarrSeriesID
				season   int
			}{
				{"zero instance", 0, 1, 0},
				{"zero series", 1, 0, 0},
				{"negative season", 1, 1, -1},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					_, err := repo.Increment(context.Background(), tc.instance, tc.series, tc.season, now)
					require.Error(t, err)
				})
			}
		})
	}
}

func TestNoBetterCounterRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			_, err := repo.Get(context.Background(), 7, 1, 1)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestNoBetterCounterRepository_Reset(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
			for i := range 3 {
				_, err := repo.Increment(ctx, 7, 122, 2, now.Add(time.Duration(i)*time.Minute))
				require.NoError(t, err)
			}

			resetAt := now.Add(time.Hour)
			require.NoError(t, repo.Reset(ctx, 7, 122, 2, resetAt))

			got, err := repo.Get(ctx, 7, 122, 2)
			require.NoError(t, err)
			assert.Equal(t, 0, got.Consecutive)
			assert.True(t, got.UpdatedAt.Equal(resetAt))
		})
	}
}

func TestNoBetterCounterRepository_Reset_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			err := repo.Reset(context.Background(), 7, 1, 1, time.Now())
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestNoBetterCounterRepository_DeleteByTriple(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			ctx := context.Background()

			_, err := repo.Increment(ctx, 7, 122, 2, time.Now().UTC())
			require.NoError(t, err)
			require.NoError(t, repo.DeleteByTriple(ctx, 7, 122, 2))

			_, err = repo.Get(ctx, 7, 122, 2)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestNoBetterCounterRepository_DeleteByTriple_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			err := repo.DeleteByTriple(context.Background(), 999, 1, 1)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestNoBetterCounterRepository_Increment_TripleIndependence(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			_, err := repo.Increment(ctx, 7, 122, 2, now)
			require.NoError(t, err)
			_, err = repo.Increment(ctx, 7, 122, 3, now) // different season
			require.NoError(t, err)
			_, err = repo.Increment(ctx, 8, 122, 2, now) // different instance
			require.NoError(t, err)

			a, err := repo.Get(ctx, 7, 122, 2)
			require.NoError(t, err)
			b, err := repo.Get(ctx, 7, 122, 3)
			require.NoError(t, err)
			c, err := repo.Get(ctx, 8, 122, 2)
			require.NoError(t, err)
			assert.Equal(t, 1, a.Consecutive)
			assert.Equal(t, 1, b.Consecutive)
			assert.Equal(t, 1, c.Consecutive)
		})
	}
}

func TestNoBetterCounterRepository_Increment_ConcurrentSameTriple(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			const N = 20
			var wg sync.WaitGroup
			wg.Add(N)
			errs := make(chan error, N)
			for range N {
				go func() {
					defer wg.Done()
					if _, err := repo.Increment(ctx, 7, 122, 2, now); err != nil {
						errs <- err
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}

			got, err := repo.Get(ctx, 7, 122, 2)
			require.NoError(t, err)
			// Under sqlite-in-memory all writes serialise; under PG the ON
			// CONFLICT DO UPDATE serialises at the row level. Either way the
			// final consecutive must equal N.
			assert.Equal(t, N, got.Consecutive,
				"upsert+expr-increment is atomic across %d concurrent calls", N)
		})
	}
}

func TestNoBetterCounterRepository_ClosedDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNoBetterCounterRepository(db)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
			_, err = repo.Increment(context.Background(), 7, 1, 1, time.Now())
			require.Error(t, err)
		})
	}
}
