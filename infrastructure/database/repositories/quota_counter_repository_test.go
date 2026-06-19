package repositories

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuotaCounterRepository_Increment_StartsAtOne(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewQuotaCounterRepository(db)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	n, err := repo.Increment(context.Background(), "omdb", w)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestQuotaCounterRepository_Increment_Accumulates(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewQuotaCounterRepository(db)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	for i := 1; i <= 5; i++ {
		n, err := repo.Increment(context.Background(), "omdb", w)
		require.NoError(t, err)
		assert.Equal(t, i, n)
	}

	got, err := repo.Get(context.Background(), "omdb", w)
	require.NoError(t, err)
	assert.Equal(t, 5, got)
}

func TestQuotaCounterRepository_DistinctServices_DistinctRows(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewQuotaCounterRepository(db)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	_, err := repo.Increment(context.Background(), "omdb", w)
	require.NoError(t, err)
	_, err = repo.Increment(context.Background(), "omdb", w)
	require.NoError(t, err)
	_, err = repo.Increment(context.Background(), "tmdb", w)
	require.NoError(t, err)

	o, err := repo.Get(context.Background(), "omdb", w)
	require.NoError(t, err)
	tm, err := repo.Get(context.Background(), "tmdb", w)
	require.NoError(t, err)
	assert.Equal(t, 2, o)
	assert.Equal(t, 1, tm)
}

func TestQuotaCounterRepository_DistinctWindows_DistinctRows(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewQuotaCounterRepository(db)
	w1 := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	w2 := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	_, _ = repo.Increment(context.Background(), "omdb", w1)
	_, _ = repo.Increment(context.Background(), "omdb", w1)
	_, _ = repo.Increment(context.Background(), "omdb", w2)

	g1, _ := repo.Get(context.Background(), "omdb", w1)
	g2, _ := repo.Get(context.Background(), "omdb", w2)
	assert.Equal(t, 2, g1)
	assert.Equal(t, 1, g2)
}

func TestQuotaCounterRepository_Get_MissingRow_ReturnsZero(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewQuotaCounterRepository(db)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	n, err := repo.Get(context.Background(), "omdb", w)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "missing row reads as zero, not ErrNotFound")
}

func TestQuotaCounterRepository_Reset_DeletesOldWindows(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewQuotaCounterRepository(db)
	old := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	cur := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	_, _ = repo.Increment(context.Background(), "omdb", old)
	_, _ = repo.Increment(context.Background(), "omdb", mid)
	_, _ = repo.Increment(context.Background(), "omdb", cur)

	// Cutoff = mid — strictly-before semantics keep `mid` and `cur`.
	deleted, err := repo.Reset(context.Background(), mid)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted, "only the `old` row is strictly before mid")

	gOld, _ := repo.Get(context.Background(), "omdb", old)
	gMid, _ := repo.Get(context.Background(), "omdb", mid)
	gCur, _ := repo.Get(context.Background(), "omdb", cur)
	assert.Equal(t, 0, gOld)
	assert.Equal(t, 1, gMid)
	assert.Equal(t, 1, gCur)
}

// TestQuotaCounterRepository_Increment_SurvivesAcrossRepos verifies the
// "pod restart preserves counter" acceptance criterion. Two repos
// against the SAME underlying file-backed (in-memory shared cache)
// sqlite see each other's writes; we simulate restart by constructing
// a second repo on the same connection.
func TestQuotaCounterRepository_Increment_SurvivesAcrossRepos(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	r1 := NewQuotaCounterRepository(db)
	_, err := r1.Increment(context.Background(), "omdb", w)
	require.NoError(t, err)
	_, err = r1.Increment(context.Background(), "omdb", w)
	require.NoError(t, err)

	// Fresh repo wrapper — analog to a restarted process that
	// opens the same DB.
	r2 := NewQuotaCounterRepository(db)
	got, err := r2.Get(context.Background(), "omdb", w)
	require.NoError(t, err)
	assert.Equal(t, 2, got, "rows persist across repository instances")
}

// TestQuotaCounterRepository_Increment_ConcurrentNoLost verifies the
// upsert is contention-safe. SQLite serialises writes via the
// single-connection pool, so the test mainly proves that goroutine
// fan-in does not double-count or drop. On Postgres the ON CONFLICT
// path is genuinely lock-free across rows.
func TestQuotaCounterRepository_Increment_ConcurrentNoLost(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewQuotaCounterRepository(db)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	const goroutines = 8
	const tries = 25
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range tries {
				_, err := repo.Increment(context.Background(), "omdb", w)
				assert.NoError(t, err)
			}
		})
	}
	wg.Wait()
	got, _ := repo.Get(context.Background(), "omdb", w)
	assert.Equal(t, goroutines*tries, got, "no lost updates")
}
