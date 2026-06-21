package persistence

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// D-0 quality bar (project_seasonfill_test_quality_bar):
//   - testcontainers Postgres + SQLite via testhelpers.AllBackends
//   - uuid/random service ids to avoid cross-case row collisions under
//     `-parallel` sharing of testcontainers Postgres
//   - error-pair coverage (missing row reads zero, no error)
//   - SetQuota / MarkExhausted prod OnConflict shapes

// newQuotaService returns a unique service id per test case so parallel
// tests sharing the same Postgres testcontainer cannot collide on the
// composite (service_name, window_start) primary key.
func newQuotaService(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, rand.Uint64())
}

func TestQuotaCounterRepository_Increment_StartsAtOne(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewQuotaCounterRepository(backend.NewDB(t))
			ctx := context.Background()
			svc := newQuotaService("omdb-start")
			w := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			n, err := repo.Increment(ctx, svc, w)
			require.NoError(t, err)
			assert.Equal(t, 1, n)
		})
	}
}

func TestQuotaCounterRepository_Increment_Accumulates(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewQuotaCounterRepository(backend.NewDB(t))
			ctx := context.Background()
			svc := newQuotaService("omdb-acc")
			w := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			for i := 1; i <= 5; i++ {
				n, err := repo.Increment(ctx, svc, w)
				require.NoError(t, err)
				assert.Equal(t, i, n)
			}
		})
	}
}

func TestQuotaCounterRepository_DistinctServices_DistinctRows(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewQuotaCounterRepository(backend.NewDB(t))
			ctx := context.Background()
			a := newQuotaService("omdb-a")
			b := newQuotaService("tmdb-b")
			w := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			_, _ = repo.Increment(ctx, a, w)
			_, _ = repo.Increment(ctx, a, w)
			_, _ = repo.Increment(ctx, b, w)

			ga, err := repo.Get(ctx, a, w)
			require.NoError(t, err)
			gb, err := repo.Get(ctx, b, w)
			require.NoError(t, err)
			assert.Equal(t, 2, ga)
			assert.Equal(t, 1, gb)
		})
	}
}

func TestQuotaCounterRepository_DistinctWindows_DistinctRows(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewQuotaCounterRepository(backend.NewDB(t))
			ctx := context.Background()
			svc := newQuotaService("omdb-windows")
			w1 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
			w2 := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			_, _ = repo.Increment(ctx, svc, w1)
			_, _ = repo.Increment(ctx, svc, w1)
			_, _ = repo.Increment(ctx, svc, w2)

			g1, _ := repo.Get(ctx, svc, w1)
			g2, _ := repo.Get(ctx, svc, w2)
			assert.Equal(t, 2, g1)
			assert.Equal(t, 1, g2)
		})
	}
}

func TestQuotaCounterRepository_Get_MissingRow_ReturnsZero(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewQuotaCounterRepository(backend.NewDB(t))
			ctx := context.Background()
			svc := newQuotaService("omdb-missing")
			w := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			n, err := repo.Get(ctx, svc, w)
			require.NoError(t, err)
			assert.Equal(t, 0, n,
				"missing row reads as zero, not ErrNotFound")
		})
	}
}

func TestQuotaCounterRepository_Reset_DeletesOldWindows(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewQuotaCounterRepository(backend.NewDB(t))
			ctx := context.Background()
			svc := newQuotaService("omdb-reset")
			old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
			mid := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
			cur := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

			_, _ = repo.Increment(ctx, svc, old)
			_, _ = repo.Increment(ctx, svc, mid)
			_, _ = repo.Increment(ctx, svc, cur)

			// Cutoff = mid — strictly-before keeps mid itself.
			deleted, err := repo.Reset(ctx, mid)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, deleted, int64(1),
				"at least the `old` row is strictly before mid")

			gOld, _ := repo.Get(ctx, svc, old)
			gMid, _ := repo.Get(ctx, svc, mid)
			gCur, _ := repo.Get(ctx, svc, cur)
			assert.Equal(t, 0, gOld)
			assert.Equal(t, 1, gMid)
			assert.Equal(t, 1, gCur)
		})
	}
}

func TestQuotaCounterRepository_Increment_SurvivesAcrossRepos(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r1 := NewQuotaCounterRepository(db)
			r2 := NewQuotaCounterRepository(db)
			ctx := context.Background()
			svc := newQuotaService("omdb-cross")
			w := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			n1, err := r1.Increment(ctx, svc, w)
			require.NoError(t, err)
			assert.Equal(t, 1, n1)

			// Different repo handle, same DB — must see the row.
			n2, err := r2.Increment(ctx, svc, w)
			require.NoError(t, err)
			assert.Equal(t, 2, n2)
		})
	}
}

func TestQuotaCounterRepository_Increment_ConcurrentNoLost(t *testing.T) {
	t.Parallel()
	// SQLite serialises writes through a single connection (we cap
	// MaxOpenConns=1 in testhelpers) so contention here is artificial,
	// but the test still exercises the ON CONFLICT path under parallel
	// callers. Postgres runs the real concurrent UPSERT.
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewQuotaCounterRepository(backend.NewDB(t))
			ctx := context.Background()
			svc := newQuotaService("omdb-concurrent")
			w := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			const goroutines = 8
			const tries = 25
			var wg sync.WaitGroup
			for range goroutines {
				wg.Go(func() {
					for range tries {
						_, _ = repo.Increment(ctx, svc, w)
					}
				})
			}
			wg.Wait()

			got, err := repo.Get(ctx, svc, w)
			require.NoError(t, err)
			assert.Equal(t, goroutines*tries, got,
				"no lost updates under concurrent contention")
		})
	}
}

// TestQuotaCounterRepository_SetQuota_StampsCap covers the D-5 466c
// port extension: after Increment seeds the row, SetQuota stamps
// requests_quota — observable via raw row read.
func TestQuotaCounterRepository_SetQuota_StampsCap(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQuotaCounterRepository(db)
			ctx := context.Background()
			svc := newQuotaService("omdb-setquota")
			w := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			_, err := repo.Increment(ctx, svc, w)
			require.NoError(t, err)

			require.NoError(t, repo.SetQuota(ctx, svc, w, 1000))

			var row database.QuotaStateModel
			require.NoError(t, db.Where(
				"service_name = ? AND window_start = ?", svc, w,
			).First(&row).Error)
			assert.Equal(t, 1000, row.RequestsQuota)
			assert.Equal(t, 1, row.RequestsMade,
				"SetQuota must not perturb requests_made")
		})
	}
}

// TestQuotaCounterRepository_SetQuota_NoopWhenRowAbsent mirrors the
// DB UPDATE WHERE rowcount=0 semantic — no error, no insert.
func TestQuotaCounterRepository_SetQuota_NoopWhenRowAbsent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQuotaCounterRepository(db)
			ctx := context.Background()
			svc := newQuotaService("omdb-setquota-absent")
			w := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			require.NoError(t, repo.SetQuota(ctx, svc, w, 1000))

			var rows []database.QuotaStateModel
			require.NoError(t, db.Where(
				"service_name = ?", svc,
			).Find(&rows).Error)
			assert.Empty(t, rows,
				"SetQuota on absent row must not INSERT")
		})
	}
}

// TestQuotaCounterRepository_MarkExhausted_Idempotent covers the
// D-5 466c WHERE exhausted_at IS NULL clause: second call must not
// overwrite the original boundary-cross timestamp.
func TestQuotaCounterRepository_MarkExhausted_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQuotaCounterRepository(db)
			ctx := context.Background()
			svc := newQuotaService("omdb-exhausted")
			w := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)

			_, err := repo.Increment(ctx, svc, w)
			require.NoError(t, err)

			require.NoError(t, repo.MarkExhausted(ctx, svc, w))
			var first database.QuotaStateModel
			require.NoError(t, db.Where(
				"service_name = ? AND window_start = ?", svc, w,
			).First(&first).Error)
			require.NotNil(t, first.ExhaustedAt, "first call must stamp")
			firstStamp := *first.ExhaustedAt

			// Sleep a hair so a Postgres clock_timestamp() bump would be
			// visible if the implementation accidentally re-stamped.
			time.Sleep(10 * time.Millisecond)
			require.NoError(t, repo.MarkExhausted(ctx, svc, w))

			var second database.QuotaStateModel
			require.NoError(t, db.Where(
				"service_name = ? AND window_start = ?", svc, w,
			).First(&second).Error)
			require.NotNil(t, second.ExhaustedAt)
			assert.True(t, second.ExhaustedAt.Equal(firstStamp),
				"second MarkExhausted must preserve original stamp (got %v, want %v)",
				*second.ExhaustedAt, firstStamp)
		})
	}
}
