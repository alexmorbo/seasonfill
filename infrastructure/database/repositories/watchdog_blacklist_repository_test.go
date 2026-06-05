package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/regrab"
)

func sampleBlacklistEntry(t *testing.T, instanceID uint, seriesID, season int) regrab.BlacklistEntry {
	t.Helper()
	e, err := regrab.NewBlacklistEntry(
		instanceID, seriesID, season, 3,
		regrab.ReasonConsecutiveNoBetter,
		time.Now().UTC(),
	)
	require.NoError(t, err)
	return e
}

func TestWatchdogBlacklistRepository_Upsert_Insert(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewWatchdogBlacklistRepository(db)
	ctx := context.Background()

	in := sampleBlacklistEntry(t, 7, 122, 2)
	require.NoError(t, repo.Upsert(ctx, in))

	got, err := repo.Find(ctx, 7, 122, 2)
	require.NoError(t, err)
	assert.Equal(t, in.InstanceID, got.InstanceID)
	assert.Equal(t, in.SeriesID, got.SeriesID)
	assert.Equal(t, in.SeasonNumber, got.SeasonNumber)
	assert.Equal(t, in.Reason, got.Reason)
	assert.Equal(t, in.Consecutive, got.Consecutive)
	assert.False(t, got.CreatedAt.IsZero())
	assert.Nil(t, got.ExpiresAt, "v1 always writes NULL ExpiresAt")
}

func TestWatchdogBlacklistRepository_Upsert_UpdatesOnConflict(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewWatchdogBlacklistRepository(db)
	ctx := context.Background()

	first := sampleBlacklistEntry(t, 7, 122, 2)
	require.NoError(t, repo.Upsert(ctx, first))

	second := sampleBlacklistEntry(t, 7, 122, 2)
	second.Consecutive = 5
	second.Reason = regrab.ReasonQbitUnreachablePersistent
	second.CreatedAt = first.CreatedAt.Add(time.Hour)
	require.NoError(t, repo.Upsert(ctx, second))

	got, err := repo.Find(ctx, 7, 122, 2)
	require.NoError(t, err)
	assert.Equal(t, 5, got.Consecutive, "newer consecutive wins")
	assert.Equal(t, regrab.ReasonQbitUnreachablePersistent, got.Reason)
}

func TestWatchdogBlacklistRepository_Find_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewWatchdogBlacklistRepository(db)
	_, err := repo.Find(context.Background(), 999, 1, 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestWatchdogBlacklistRepository_DeleteByTriple(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewWatchdogBlacklistRepository(db)
	ctx := context.Background()

	in := sampleBlacklistEntry(t, 7, 122, 2)
	require.NoError(t, repo.Upsert(ctx, in))
	require.NoError(t, repo.DeleteByTriple(ctx, 7, 122, 2))

	_, err := repo.Find(ctx, 7, 122, 2)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestWatchdogBlacklistRepository_DeleteByTriple_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewWatchdogBlacklistRepository(db)
	err := repo.DeleteByTriple(context.Background(), 999, 1, 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestWatchdogBlacklistRepository_ListByInstance(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewWatchdogBlacklistRepository(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	older, err := regrab.NewBlacklistEntry(7, 100, 1, 3, regrab.ReasonConsecutiveNoBetter, now.Add(-time.Hour))
	require.NoError(t, err)
	newer, err := regrab.NewBlacklistEntry(7, 200, 2, 3, regrab.ReasonConsecutiveNoBetter, now)
	require.NoError(t, err)
	other, err := regrab.NewBlacklistEntry(8, 300, 1, 3, regrab.ReasonConsecutiveNoBetter, now)
	require.NoError(t, err)
	require.NoError(t, repo.Upsert(ctx, older))
	require.NoError(t, repo.Upsert(ctx, newer))
	require.NoError(t, repo.Upsert(ctx, other))

	rows, err := repo.ListByInstance(ctx, 7)
	require.NoError(t, err)
	require.Len(t, rows, 2, "must include only instance 7 rows")
	assert.Equal(t, 200, rows[0].SeriesID, "newest first")
	assert.Equal(t, 100, rows[1].SeriesID)
}

func TestWatchdogBlacklistRepository_ListByInstance_Empty(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewWatchdogBlacklistRepository(db)
	rows, err := repo.ListByInstance(context.Background(), 999)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestWatchdogBlacklistRepository_TripleUniqueness(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewWatchdogBlacklistRepository(db)
	ctx := context.Background()

	// Same series id on a different instance is a separate row.
	require.NoError(t, repo.Upsert(ctx, sampleBlacklistEntry(t, 7, 122, 2)))
	require.NoError(t, repo.Upsert(ctx, sampleBlacklistEntry(t, 8, 122, 2)))

	a, err := repo.Find(ctx, 7, 122, 2)
	require.NoError(t, err)
	b, err := repo.Find(ctx, 8, 122, 2)
	require.NoError(t, err)
	assert.Equal(t, uint(7), a.InstanceID)
	assert.Equal(t, uint(8), b.InstanceID)
}

func TestWatchdogBlacklistRepository_ClosedDB(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewWatchdogBlacklistRepository(db)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	err = repo.Upsert(context.Background(), sampleBlacklistEntry(t, 7, 1, 1))
	require.Error(t, err)
}
