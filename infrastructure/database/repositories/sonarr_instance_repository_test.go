package repositories

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
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
	require.NoError(t, repo.UpdateWithOptions(ctx, inst, cipher, true, nil))

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
	require.NoError(t, repo.UpdateWithOptions(ctx, inst, cipher, false, nil))

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
		Name:      "intcheck",
		URL:       "http://sonarr.local",
		APIKey:    "k",
		Mode:      "auto",
		Timeout:   10 * time.Second,
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
	require.NoError(t, repo.UpdateWithOptions(ctx, inst, cipher, false, nil))

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
	require.NoError(t, repo.UpdateWithOptions(ctx, inst, cipher, false, nil))

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
	err = repo.UpdateWithOptions(ctx, inst, cipher, false, nil)
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

// --- 028h-2: N+1 elimination tests ---

// queryCounter installs a GORM "after query" callback that increments
// `*counter` on every executed SELECT statement. Returns a cleanup
// closure that removes the callback. The callback name is unique so
// concurrent tests don't collide.
func queryCounter(t *testing.T, db *gorm.DB) (*int64, func()) {
	t.Helper()
	var n int64
	cbName := "test-count-" + t.Name()
	err := db.Callback().Query().After("gorm:query").
		Register(cbName, func(tx *gorm.DB) {
			atomic.AddInt64(&n, 1)
		})
	require.NoError(t, err)
	return &n, func() {
		_ = db.Callback().Query().Remove(cbName)
	}
}

// seedInstances creates `count` instances with predictable names
// (inst-0, inst-1, ...) and unique api_keys (key-0, key-1, ...).
func seedInstances(t *testing.T, repo *SonarrInstanceRepository, c *crypto.Cipher, count int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < count; i++ {
		inst := runtime.InstanceSnapshot{
			Name:    fmt.Sprintf("inst-%d", i),
			URL:     "http://sonarr.local",
			APIKey:  fmt.Sprintf("key-%d", i),
			Mode:    "auto",
			Timeout: 10 * time.Second,
		}
		_, err := repo.Create(ctx, inst, c)
		require.NoError(t, err)
	}
}

func TestList_NoNPlusOne_TwoInstances(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	seedInstances(t, repo, cipher, 2)

	// Install the counter AFTER seed so Create's internal queries
	// don't pollute the count.
	count, cleanup := queryCounter(t, db)
	defer cleanup()

	out, err := repo.List(context.Background(), cipher)
	require.NoError(t, err)
	assert.Len(t, out, 2)
	assert.Equal(t, int64(2), atomic.LoadInt64(count),
		"List must issue EXACTLY 2 SELECT queries for any N (got %d for N=2)",
		atomic.LoadInt64(count))
}

func TestList_NoNPlusOne_TenInstances(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	seedInstances(t, repo, cipher, 10)

	count, cleanup := queryCounter(t, db)
	defer cleanup()

	out, err := repo.List(context.Background(), cipher)
	require.NoError(t, err)
	assert.Len(t, out, 10)
	// EXACT 2 — anything higher means the N+1 regressed.
	assert.Equal(t, int64(2), atomic.LoadInt64(count),
		"List must issue EXACTLY 2 SELECT queries for any N (got %d for N=10)",
		atomic.LoadInt64(count))
}

func TestList_PreservesAPIKeyDecryption(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	seedInstances(t, repo, cipher, 3)

	out, err := repo.List(context.Background(), cipher)
	require.NoError(t, err)
	require.Len(t, out, 3)

	// Build a name -> apiKey map (order is not guaranteed) and
	// assert each instance carries its OWN key, not a neighbour's.
	got := map[string]string{}
	for _, s := range out {
		got[s.Name] = s.APIKey
	}
	assert.Equal(t, "key-0", got["inst-0"])
	assert.Equal(t, "key-1", got["inst-1"])
	assert.Equal(t, "key-2", got["inst-2"])
}

func TestList_HandlesMissingSecret(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	// Create one instance WITH a secret, one without.
	_, err = repo.Create(context.Background(), runtime.InstanceSnapshot{
		Name: "has-key", URL: "http://x", APIKey: "k", Mode: "auto", Timeout: 10 * time.Second,
	}, cipher)
	require.NoError(t, err)

	// Insert an instance row directly via GORM bypassing repo.Create
	// so no secret row is written.
	naked := database.SonarrInstanceModel{
		Name: "no-key", URL: "http://x", Mode: "auto", TimeoutSeconds: 10,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, db.Create(&naked).Error)

	out, err := repo.List(context.Background(), cipher)
	require.NoError(t, err)
	require.Len(t, out, 2)

	got := map[string]string{}
	for _, s := range out {
		got[s.Name] = s.APIKey
	}
	assert.Equal(t, "k", got["has-key"])
	assert.Equal(t, "", got["no-key"],
		"instance with no secret row must surface empty APIKey, not error")
}

// --- 041a: Phase 11 instance fields round-trip ---

func TestSonarrInstanceRepository_PublicURL_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	public := "https://sonarr.example.com"
	inst := runtime.InstanceSnapshot{
		Name: "pub", URL: "http://sonarr.local", APIKey: "k", Mode: "auto",
		Timeout:   10 * time.Second,
		PublicURL: &public,
	}
	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)
	assert.Greater(t, id, uint(0))

	got, err := repo.GetByName(ctx, "pub", cipher)
	require.NoError(t, err)
	require.NotNil(t, got.PublicURL, "PublicURL must round-trip non-nil")
	assert.Equal(t, public, *got.PublicURL)
}

func TestSonarrInstanceRepository_PublicURL_NilByDefault(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name: "nopub", URL: "http://sonarr.local", APIKey: "k", Mode: "auto",
		Timeout: 10 * time.Second,
	}
	_, err = repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	got, err := repo.GetByName(ctx, "nopub", cipher)
	require.NoError(t, err)
	assert.Nil(t, got.PublicURL, "absent override must remain nil")
}

func TestSonarrInstanceRepository_WebhookInstallEnabled_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name: "wh", URL: "http://sonarr.local", APIKey: "k", Mode: "auto",
		Timeout:               10 * time.Second,
		WebhookInstallEnabled: true,
	}
	id, err := repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	got, err := repo.GetByName(ctx, "wh", cipher)
	require.NoError(t, err)
	assert.True(t, got.WebhookInstallEnabled)

	// Flip to false via an Update and re-read.
	inst.ID = id
	inst.APIKey = ""
	inst.WebhookInstallEnabled = false
	require.NoError(t, repo.UpdateWithOptions(ctx, inst, cipher, true, nil))

	got, err = repo.GetByName(ctx, "wh", cipher)
	require.NoError(t, err)
	assert.False(t, got.WebhookInstallEnabled,
		"webhook_install_enabled must persist as false (zero-value-bug guard)")
}

func TestSonarrInstanceRepository_WebhookURLOverride_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSonarrInstanceRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	override := "https://seasonfill.example.com"
	inst := runtime.InstanceSnapshot{
		Name: "ovr", URL: "http://sonarr.local", APIKey: "k", Mode: "auto",
		Timeout:            10 * time.Second,
		WebhookURLOverride: &override,
	}
	_, err = repo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	got, err := repo.GetByName(ctx, "ovr", cipher)
	require.NoError(t, err)
	require.NotNil(t, got.WebhookURLOverride)
	assert.Equal(t, override, *got.WebhookURLOverride)
}

func TestSonarrInstanceRepository_Delete_PurgesSeriesCache(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	instRepo := NewSonarrInstanceRepository(db)
	cacheRepo := NewSeriesCacheRepository(db)
	ctx := context.Background()

	cipher, err := crypto.New("test-master-key-12345")
	require.NoError(t, err)

	inst := runtime.InstanceSnapshot{
		Name:    "main",
		URL:     "http://sonarr.local",
		APIKey:  "secret",
		Mode:    "auto",
		Timeout: 10 * time.Second,
	}
	_, err = instRepo.Create(ctx, inst, cipher)
	require.NoError(t, err)

	sampleEntry := series.CacheEntry{
		InstanceName:   "main",
		SonarrSeriesID: 12,
		Title:          "Test Series",
		TitleSlug:      "test-series",
	}
	require.NoError(t, cacheRepo.Upsert(ctx, sampleEntry))
	sampleEntry.SonarrSeriesID = 13
	require.NoError(t, cacheRepo.Upsert(ctx, sampleEntry))

	require.NoError(t, instRepo.Delete(ctx, "main"))

	got, err := cacheRepo.ListActiveByInstance(ctx, "main")
	require.NoError(t, err)
	assert.Empty(t, got)
}
