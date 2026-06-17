package repositories

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppSettingsRepository_GetTimezone_SeededNull(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewAppSettingsRepository(db)

	tz, err := repo.GetTimezone(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "", tz, "fresh DB: seeded row has NULL timezone → empty string")
}

func TestAppSettingsRepository_SetThenGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewAppSettingsRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.SetTimezone(ctx, "Europe/Moscow"))

	got, err := repo.GetTimezone(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Europe/Moscow", got)
}

func TestAppSettingsRepository_SetEmpty_ClearsToNull(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewAppSettingsRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.SetTimezone(ctx, "America/New_York"))
	require.NoError(t, repo.SetTimezone(ctx, ""))

	got, err := repo.GetTimezone(ctx)
	require.NoError(t, err)
	assert.Equal(t, "", got, "empty SetTimezone should clear column to NULL")
}

func TestAppSettingsRepository_SetTimezone_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewAppSettingsRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.SetTimezone(ctx, "UTC"))
	require.NoError(t, repo.SetTimezone(ctx, "UTC"))
	require.NoError(t, repo.SetTimezone(ctx, "UTC"))

	got, err := repo.GetTimezone(ctx)
	require.NoError(t, err)
	assert.Equal(t, "UTC", got)
}
