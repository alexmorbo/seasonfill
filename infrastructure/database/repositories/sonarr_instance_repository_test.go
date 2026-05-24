package repositories

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
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

func TestSonarrInstanceRepository_UpdatePreservesBoolFalse(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name:    "boolcheck",
		URL:     "http://sonarr.local",
		APIKey:  "k",
		Mode:    "auto",
		Timeout: 10 * time.Second,
		Search: runtime.SearchSnapshot{
			RequireAllAired: true,
			SkipSpecials:    true,
			SkipAnime:       true,
		},
		Ranking: runtime.RankingSnapshot{IndexerPriorityEnabled: true},
	}
	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	// Now flip every bool to false and update.
	inst.ID = id
	inst.APIKey = ""
	inst.Search.RequireAllAired = false
	inst.Search.SkipSpecials = false
	inst.Search.SkipAnime = false
	inst.Ranking.IndexerPriorityEnabled = false
	require.NoError(t, repo.Update(ctx, inst, cipher))

	got, err := repo.GetByName(ctx, "boolcheck", cipher)
	require.NoError(t, err)
	assert.False(t, got.Search.RequireAllAired, "SkipAnime/etc. peer: require_all_aired should persist as false")
	assert.False(t, got.Search.SkipSpecials)
	assert.False(t, got.Search.SkipAnime, "the canonical zero-value-bug field")
	assert.False(t, got.Ranking.IndexerPriorityEnabled)
}

func TestSonarrInstanceRepository_UpdatePreservesInt0(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name:    "intcheck",
		URL:     "http://sonarr.local",
		APIKey:  "k",
		Mode:    "auto",
		Timeout: 10 * time.Second,
		RateLimit: runtime.RateLimitSnapshot{RPM: 30, Burst: 10},
		Limits: runtime.LimitsSnapshot{
			ScanMaxSeries:   42,
			MaxGrabsPerScan: 7,
		},
		Search: runtime.SearchSnapshot{MinCustomFormatScore: 15},
		Retry: runtime.RetrySnapshot{
			MaxAttempts:    5,
			InitialBackoff: 2 * time.Second,
			MaxBackoff:     30 * time.Second,
		},
	}
	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	// Zero every numeric field and update.
	inst.ID = id
	inst.APIKey = ""
	inst.RateLimit.RPM = 0
	inst.RateLimit.Burst = 0
	inst.Limits.ScanMaxSeries = 0
	inst.Limits.MaxGrabsPerScan = 0
	inst.Search.MinCustomFormatScore = 0
	inst.Retry.MaxAttempts = 0
	inst.Retry.InitialBackoff = 0
	inst.Retry.MaxBackoff = 0
	require.NoError(t, repo.Update(ctx, inst, cipher))

	got, err := repo.GetByName(ctx, "intcheck", cipher)
	require.NoError(t, err)
	assert.Equal(t, 0, got.RateLimit.RPM, "rate_limit_rpm must persist as 0 (was 30)")
	assert.Equal(t, 0, got.RateLimit.Burst)
	assert.Equal(t, 0, got.Limits.ScanMaxSeries)
	assert.Equal(t, 0, got.Limits.MaxGrabsPerScan)
	assert.Equal(t, 0, got.Search.MinCustomFormatScore)
	assert.Equal(t, 0, got.Retry.MaxAttempts)
	assert.Equal(t, time.Duration(0), got.Retry.InitialBackoff)
	assert.Equal(t, time.Duration(0), got.Retry.MaxBackoff)
}

func TestSonarrInstanceRepository_UpdatePreservesStringEmpty(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name:    "strcheck",
		URL:     "http://sonarr.local",
		APIKey:  "k",
		Mode:    "auto",
		Timeout: 10 * time.Second,
		Tags: runtime.TagsSnapshot{
			Mode:    "include",
			Include: []string{"a", "b"},
		},
		Cooldown: runtime.CooldownSnapshot{Mode: "smart"},
	}
	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	// Empty the string fields and update.
	inst.ID = id
	inst.APIKey = ""
	inst.Tags.Mode = ""
	inst.Cooldown.Mode = ""
	require.NoError(t, repo.Update(ctx, inst, cipher))

	got, err := repo.GetByName(ctx, "strcheck", cipher)
	require.NoError(t, err)
	assert.Equal(t, "", got.Tags.Mode, "tags.mode must persist as empty (was 'include')")
	assert.Equal(t, "", got.Cooldown.Mode)
}

func TestSonarrInstanceRepository_UpdateMissingRow_ReturnsNotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	// Build a snapshot for an ID that does not exist.
	inst := runtime.InstanceSnapshot{
		ID:      9999,
		Name:    "ghost",
		URL:     "http://x",
		Mode:    "auto",
		Timeout: 10 * time.Second,
	}
	err = repo.Update(ctx, inst, cipher)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestSonarrInstanceRepository_Update_StaleIUS_Rejects(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name: "ius", URL: "http://x", APIKey: "k", Mode: "auto",
		Timeout: 10 * time.Second,
	}
	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	// Read the stored timestamp, then pretend the client snapshot
	// was taken one second before. That makes the stored row strictly
	// newer than the header → precondition fail.
	stored, err := repo.GetUpdatedAt(ctx, "ius")
	require.NoError(t, err)
	staleHeader := stored.Add(-1 * time.Second)

	inst.ID = id
	inst.APIKey = ""
	inst.Mode = "manual"
	err = repo.UpdateWithOptions(ctx, inst, cipher, true, &staleHeader)
	require.ErrorIs(t, err, ports.ErrStaleWrite)

	// Confirm the row was NOT mutated.
	got, err := repo.GetByName(ctx, "ius", cipher)
	require.NoError(t, err)
	assert.Equal(t, "auto", got.Mode, "stale write must not persist")
}

func TestSonarrInstanceRepository_Update_FreshIUS_Writes(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name: "ius2", URL: "http://x", APIKey: "k", Mode: "auto",
		Timeout: 10 * time.Second,
	}
	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	stored, err := repo.GetUpdatedAt(ctx, "ius2")
	require.NoError(t, err)
	// Header equal to stored (second-truncated) → accepted.
	fresh := stored.Truncate(time.Second)

	inst.ID = id
	inst.APIKey = ""
	inst.Mode = "manual"
	err = repo.UpdateWithOptions(ctx, inst, cipher, true, &fresh)
	require.NoError(t, err)

	got, err := repo.GetByName(ctx, "ius2", cipher)
	require.NoError(t, err)
	assert.Equal(t, "manual", got.Mode)
}

func TestSonarrInstanceRepository_Create_TimestampsMatch(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name: "ts", URL: "http://x", APIKey: "secret", Mode: "auto",
		Timeout: 10 * time.Second,
	}
	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	// Read parent updated_at + secret updated_at. With a single
	// time.Now() inside the tx they must match exactly.
	var parent database.SonarrInstanceModel
	require.NoError(t, db.Select("created_at", "updated_at").
		Where("id = ?", id).First(&parent).Error)
	var secret database.InstanceSecretModel
	require.NoError(t, db.Select("created_at", "updated_at").
		Where("instance_id = ? AND secret_name = ?", id, "api_key").
		First(&secret).Error)
	assert.True(t, parent.CreatedAt.Equal(secret.CreatedAt),
		"parent and secret CreatedAt must match (single time.Now() in tx)")
	assert.True(t, parent.UpdatedAt.Equal(secret.UpdatedAt),
		"parent and secret UpdatedAt must match")
}

func TestSonarrInstanceRepository_Update_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		ID: 9999, Name: "ghost", URL: "http://x", Mode: "auto",
		Timeout: 10 * time.Second,
	}
	now := time.Now().UTC()
	err = repo.UpdateWithOptions(ctx, inst, cipher, true, &now)
	require.ErrorIs(t, err, ports.ErrNotFound,
		"missing row must return ErrNotFound, not ErrStaleWrite")
}

func TestSonarrInstanceRepository_Update_ConcurrentIUS(t *testing.T) {
	db := setupTestDB(t)
	// Limit the pool to a single connection so both goroutines share the
	// same SQLite in-memory database instance (multiple connections would
	// each open an independent ":memory:" database).
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name: "race", URL: "http://x", APIKey: "k", Mode: "auto",
		Timeout: 10 * time.Second,
	}
	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	stored, err := repo.GetUpdatedAt(ctx, "race")
	require.NoError(t, err)
	header := stored.Truncate(time.Second)
	// Sleep one second so any write produces strictly-newer updated_at.
	time.Sleep(1100 * time.Millisecond)

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			snap := inst
			snap.ID = id
			snap.APIKey = ""
			snap.Mode = "manual"
			results[idx] = repo.UpdateWithOptions(ctx, snap, cipher, true, &header)
		}(i)
	}
	wg.Wait()

	var ok, stale int
	for _, e := range results {
		switch {
		case e == nil:
			ok++
		case errors.Is(e, ports.ErrStaleWrite):
			stale++
		default:
			t.Fatalf("unexpected error: %v", e)
		}
	}
	assert.GreaterOrEqual(t, ok, 1, "at least one writer must succeed")
	assert.Equal(t, 2, ok+stale, "every outcome must be success or stale")
}
