package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/cooldown"
)

func TestCooldownRepository_Set_Get(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCooldownRepository(db)
	ctx := context.Background()
	exp := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	require.NoError(t, repo.Set(ctx, cooldown.Cooldown{
		Scope: cooldown.ScopeGUID, Key: "g1", ExpiresAt: exp, Reason: "test",
	}))
	c, ok, err := repo.Get(ctx, cooldown.ScopeGUID, "g1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, cooldown.ScopeGUID, c.Scope)
	assert.Equal(t, "g1", c.Key)
}

func TestCooldownRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCooldownRepository(db)
	_, ok, err := repo.Get(context.Background(), cooldown.ScopeGUID, "missing")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCooldownRepository_Set_UpdatesOnConflict(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCooldownRepository(db)
	ctx := context.Background()
	exp1 := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	require.NoError(t, repo.Set(ctx, cooldown.Cooldown{
		Scope: cooldown.ScopeSeries, Key: "main:1:1", ExpiresAt: exp1, Reason: "first",
	}))
	exp2 := exp1.Add(2 * time.Hour)
	require.NoError(t, repo.Set(ctx, cooldown.Cooldown{
		Scope: cooldown.ScopeSeries, Key: "main:1:1", ExpiresAt: exp2, Reason: "second",
	}))
	c, ok, err := repo.Get(ctx, cooldown.ScopeSeries, "main:1:1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "second", c.Reason)
	assert.True(t, c.ExpiresAt.Equal(exp2) || c.ExpiresAt.After(exp1))
}

func TestCooldownRepository_FilterActive(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCooldownRepository(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.Set(ctx, cooldown.Cooldown{
		Scope: cooldown.ScopeGUID, Key: "active1", ExpiresAt: now.Add(time.Hour), Reason: "x",
	}))
	require.NoError(t, repo.Set(ctx, cooldown.Cooldown{
		Scope: cooldown.ScopeGUID, Key: "expired1", ExpiresAt: now.Add(-time.Hour), Reason: "x",
	}))
	res, err := repo.FilterActive(ctx, cooldown.ScopeGUID, []string{"active1", "expired1", "absent"}, now)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "active1", res[0].Key)
}

func TestCooldownRepository_FilterActive_EmptyKeys(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCooldownRepository(db)
	res, err := repo.FilterActive(context.Background(), cooldown.ScopeGUID, nil, time.Now().UTC())
	require.NoError(t, err)
	assert.Empty(t, res)
}

func TestCooldownRepository_Sweep(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCooldownRepository(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.Set(ctx, cooldown.Cooldown{Scope: cooldown.ScopeGUID, Key: "old1", ExpiresAt: now.Add(-time.Hour)}))
	require.NoError(t, repo.Set(ctx, cooldown.Cooldown{Scope: cooldown.ScopeGUID, Key: "old2", ExpiresAt: now.Add(-time.Minute)}))
	require.NoError(t, repo.Set(ctx, cooldown.Cooldown{Scope: cooldown.ScopeGUID, Key: "future", ExpiresAt: now.Add(time.Hour)}))

	n, err := repo.Sweep(ctx, now)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	_, ok, _ := repo.Get(ctx, cooldown.ScopeGUID, "future")
	assert.True(t, ok)
}

func TestCooldownRepository_Set_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCooldownRepository(db)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	err = repo.Set(context.Background(), cooldown.Cooldown{Scope: cooldown.ScopeGUID, Key: "x", ExpiresAt: time.Now().Add(time.Hour)})
	require.Error(t, err)
}
