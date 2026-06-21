package persistence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedStateInstance creates a sonarr_instance row so the FK CASCADE
// constraint on watchdog_state passes on Postgres.
func seedStateInstance(t *testing.T, ctx context.Context, repo *WatchdogStateRepository, name domain.InstanceName) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, repo.db.WithContext(ctx).Save(&database.SonarrInstanceModel{
		Name:      string(name),
		URL:       "http://" + string(name),
		Mode:      "managed",
		CreatedAt: now,
		UpdatedAt: now,
	}).Error)
}

func TestWatchdogState_Increment_FirstContact(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")

			now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
			got, err := repo.Increment(ctx, "homelab", 122, 2, now)
			require.NoError(t, err)
			assert.Equal(t, domain.InstanceName("homelab"), got.InstanceName)
			assert.Equal(t, domain.SonarrSeriesID(122), got.SonarrSeriesID)
			assert.Equal(t, 2, got.SeasonNumber)
			assert.Equal(t, 1, got.AttemptCount)
			assert.True(t, got.LastAttemptAt.Equal(now))
			assert.True(t, got.UpdatedAt.Equal(now))
			assert.Nil(t, got.CooldownUntil)
			assert.Nil(t, got.LastError)
		})
	}
}

func TestWatchdogState_Increment_ExistingTriple(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")

			first := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
			_, err := repo.Increment(ctx, "homelab", 122, 2, first)
			require.NoError(t, err)

			second := first.Add(time.Hour)
			got, err := repo.Increment(ctx, "homelab", 122, 2, second)
			require.NoError(t, err)
			assert.Equal(t, 2, got.AttemptCount)
			assert.True(t, got.LastAttemptAt.Equal(second))
			assert.True(t, got.UpdatedAt.Equal(second))
		})
	}
}

func TestWatchdogState_Increment_RejectsInvalidTriple(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			now := time.Now().UTC()

			cases := []struct {
				name     string
				instance domain.InstanceName
				series   domain.SonarrSeriesID
				season   int
			}{
				{"empty instance", "", 1, 0},
				{"zero series", "homelab", 0, 0},
				{"negative season", "homelab", 1, -1},
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

func TestWatchdogState_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			_, err := repo.Get(context.Background(), "ghost", 1, 1)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestWatchdogState_Reset(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")

			now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
			for i := range 3 {
				_, err := repo.Increment(ctx, "homelab", 122, 2, now.Add(time.Duration(i)*time.Minute))
				require.NoError(t, err)
			}

			resetAt := now.Add(time.Hour)
			require.NoError(t, repo.Reset(ctx, "homelab", 122, 2, resetAt))

			got, err := repo.Get(ctx, "homelab", 122, 2)
			require.NoError(t, err)
			assert.Equal(t, 0, got.AttemptCount)
			assert.True(t, got.UpdatedAt.Equal(resetAt))
		})
	}
}

func TestWatchdogState_Reset_PreservesCooldownUntil(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")

			now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
			_, err := repo.Increment(ctx, "homelab", 122, 2, now)
			require.NoError(t, err)

			future := now.Add(24 * time.Hour)
			require.NoError(t, repo.SetCooldownUntil(ctx, "homelab", 122, 2, future))

			require.NoError(t, repo.Reset(ctx, "homelab", 122, 2, now.Add(time.Hour)))

			got, err := repo.Get(ctx, "homelab", 122, 2)
			require.NoError(t, err)
			assert.Equal(t, 0, got.AttemptCount)
			require.NotNil(t, got.CooldownUntil, "Reset must preserve cooldown_until")
			assert.True(t, got.CooldownUntil.Equal(future))
		})
	}
}

func TestWatchdogState_Reset_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			err := repo.Reset(context.Background(), "ghost", 1, 1, time.Now())
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestWatchdogState_SetCooldownUntil_MissingRow_ErrNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			err := repo.SetCooldownUntil(context.Background(), "ghost", 1, 1, time.Now())
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestWatchdogState_SetLastError(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")

			now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
			_, err := repo.Increment(ctx, "homelab", 122, 2, now)
			require.NoError(t, err)

			require.NoError(t, repo.SetLastError(ctx, "homelab", 122, 2, "sonarr 503"))
			got, err := repo.Get(ctx, "homelab", 122, 2)
			require.NoError(t, err)
			require.NotNil(t, got.LastError)
			assert.Equal(t, "sonarr 503", *got.LastError)
		})
	}
}

func TestWatchdogState_SetLastError_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			err := repo.SetLastError(context.Background(), "ghost", 1, 1, "oops")
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestWatchdogState_DeleteByTriple(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")

			_, err := repo.Increment(ctx, "homelab", 122, 2, time.Now().UTC())
			require.NoError(t, err)
			require.NoError(t, repo.DeleteByTriple(ctx, "homelab", 122, 2))

			_, err = repo.Get(ctx, "homelab", 122, 2)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestWatchdogState_DeleteByTriple_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			err := repo.DeleteByTriple(context.Background(), "ghost", 1, 1)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestWatchdogState_ListByInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")
			seedStateInstance(t, ctx, repo, "4k")
			now := time.Now().UTC().Truncate(time.Second)

			_, err := repo.Increment(ctx, "homelab", 100, 1, now.Add(-time.Hour))
			require.NoError(t, err)
			_, err = repo.Increment(ctx, "homelab", 200, 2, now)
			require.NoError(t, err)
			_, err = repo.Increment(ctx, "4k", 300, 1, now)
			require.NoError(t, err)

			rows, err := repo.ListByInstance(ctx, "homelab")
			require.NoError(t, err)
			require.Len(t, rows, 2, "only homelab rows")
			// Newest first by updated_at.
			assert.Equal(t, domain.SonarrSeriesID(200), rows[0].SonarrSeriesID)
		})
	}
}

func TestWatchdogState_ListByInstance_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			rows, err := repo.ListByInstance(context.Background(), "ghost")
			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}

func TestWatchdogState_ListCooldownsDue_FiltersFuture(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")
			now := time.Now().UTC().Truncate(time.Second)

			// Past cooldown — due.
			_, err := repo.Increment(ctx, "homelab", 100, 1, now.Add(-2*time.Hour))
			require.NoError(t, err)
			require.NoError(t, repo.SetCooldownUntil(ctx, "homelab", 100, 1, now.Add(-time.Hour)))

			// Future cooldown — not due.
			_, err = repo.Increment(ctx, "homelab", 200, 1, now)
			require.NoError(t, err)
			require.NoError(t, repo.SetCooldownUntil(ctx, "homelab", 200, 1, now.Add(time.Hour)))

			// NULL cooldown — not due.
			_, err = repo.Increment(ctx, "homelab", 300, 1, now)
			require.NoError(t, err)

			rows, err := repo.ListCooldownsDue(ctx, "homelab", now)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, domain.SonarrSeriesID(100), rows[0].SonarrSeriesID)
		})
	}
}

func TestWatchdogState_Increment_TripleIndependence(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")
			seedStateInstance(t, ctx, repo, "4k")
			now := time.Now().UTC()

			_, err := repo.Increment(ctx, "homelab", 122, 2, now)
			require.NoError(t, err)
			_, err = repo.Increment(ctx, "homelab", 122, 3, now) // different season
			require.NoError(t, err)
			_, err = repo.Increment(ctx, "4k", 122, 2, now) // different instance
			require.NoError(t, err)

			a, err := repo.Get(ctx, "homelab", 122, 2)
			require.NoError(t, err)
			b, err := repo.Get(ctx, "homelab", 122, 3)
			require.NoError(t, err)
			c, err := repo.Get(ctx, "4k", 122, 2)
			require.NoError(t, err)
			assert.Equal(t, 1, a.AttemptCount)
			assert.Equal(t, 1, b.AttemptCount)
			assert.Equal(t, 1, c.AttemptCount)
		})
	}
}

// TestWatchdogState_Increment_AtomicConcurrent — D-0 critical test.
// Spawn N goroutines hammering Increment on the same triple; final
// attempt_count must equal N (no lost updates). Verifies the
// gorm.Expr atomic increment under contention.
func TestWatchdogState_Increment_AtomicConcurrent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			ctx := context.Background()
			seedStateInstance(t, ctx, repo, "homelab")
			now := time.Now().UTC()

			const N = 20
			var wg sync.WaitGroup
			wg.Add(N)
			errs := make(chan error, N)
			for range N {
				go func() {
					defer wg.Done()
					if _, err := repo.Increment(ctx, "homelab", 122, 2, now); err != nil {
						errs <- err
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}

			got, err := repo.Get(ctx, "homelab", 122, 2)
			require.NoError(t, err)
			assert.Equal(t, N, got.AttemptCount,
				"upsert+expr-increment is atomic across %d concurrent calls", N)
		})
	}
}

func TestWatchdogState_ClosedDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogStateRepository(db)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
			_, err = repo.Increment(context.Background(), "homelab", 1, 1, time.Now())
			require.Error(t, err)
		})
	}
}
