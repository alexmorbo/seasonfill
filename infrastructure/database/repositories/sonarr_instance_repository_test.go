package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

func TestSonarrInstanceRepository_CreateAndGet(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name:          "main",
		URL:           "http://sonarr.local:8989",
		APIKey:        "secret-api-key",
		Mode:          "auto",
		Timeout:       10 * time.Second,
		SearchTimeout: 60 * time.Second,
		Tags: runtime.TagsSnapshot{
			Mode:    "include",
			Include: []string{"tv", "anime"},
		},
		RateLimit: runtime.RateLimitSnapshot{RPM: 30, Burst: 10},
	}

	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)
	assert.Greater(t, id, uint(0))

	retrieved, err := repo.GetByName(ctx, "main", cipher)
	require.NoError(t, err)
	assert.Equal(t, inst.Name, retrieved.Name)
	assert.Equal(t, inst.URL, retrieved.URL)
	assert.Equal(t, inst.APIKey, retrieved.APIKey)
	assert.Equal(t, inst.Mode, retrieved.Mode)
}

func TestSonarrInstanceRepository_GetNotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	_, err = repo.GetByName(ctx, "nonexistent", cipher)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestSonarrInstanceRepository_UpdatePreservesAPIKey(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name:    "test",
		URL:     "http://sonarr.local",
		APIKey:  "original-key",
		Mode:    "auto",
		Timeout: 10 * time.Second,
	}

	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	// Update without changing API key (empty string)
	inst.ID = id
	inst.APIKey = ""
	inst.Mode = "manual"
	require.NoError(t, repo.Update(ctx, inst, cipher))

	// Verify the mode changed but API key was preserved
	retrieved, err := repo.GetByName(ctx, "test", cipher)
	require.NoError(t, err)
	assert.Equal(t, "manual", retrieved.Mode)
	assert.Equal(t, "original-key", retrieved.APIKey)
}

func TestSonarrInstanceRepository_DeleteCascades(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name:    "todelete",
		URL:     "http://sonarr.local",
		APIKey:  "secret",
		Mode:    "auto",
		Timeout: 10 * time.Second,
	}

	_, err = repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	require.NoError(t, repo.Delete(ctx, "todelete"))

	_, err = repo.GetByName(ctx, "todelete", cipher)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestSonarrInstanceRepository_Count(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	inst := runtime.InstanceSnapshot{
		Name:    "instance1",
		URL:     "http://sonarr.local",
		APIKey:  "key",
		Mode:    "auto",
		Timeout: 10 * time.Second,
	}
	_, err = repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	count, err = repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
