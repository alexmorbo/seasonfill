//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	grab "github.com/alexmorbo/seasonfill/internal/grab/app"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	watchdogpersistence "github.com/alexmorbo/seasonfill/internal/watchdog/persistence"
)

type capturedGrab struct {
	GUID      string `json:"guid"`
	IndexerID int    `json:"indexerId"`
}

func TestIntegration_RealGrab_PostsAndPersists(t *testing.T) {
	t.Skip("pending D-6 grab+watchdog rewrite — D2-revised-roadmap.md")
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/system/status", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "system-status.json"))
	})
	mux.HandleFunc("/api/v3/series", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[`))
		_, _ = w.Write(loadFixture(t, "series-122-detail.json"))
		_, _ = w.Write([]byte(`]`))
	})
	mux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "episodes-s122-s2.json"))
	})
	mux.HandleFunc("/api/v3/episodeFile", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "episodefile-s122.json"))
	})
	mux.HandleFunc("/api/v3/qualityprofile/14", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "qualityprofile-14.json"))
	})
	mux.HandleFunc("/api/v3/indexer", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "indexer-list.json"))
	})
	mux.HandleFunc("/api/v3/tag", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "tag-list.json"))
	})
	mux.HandleFunc("/api/v3/history", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "history-s122-grabbed.json"))
	})

	var (
		mu        sync.Mutex
		postCount int
		gotPosts  []capturedGrab
	)
	mux.HandleFunc("/api/v3/release", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write(loadFixture(t, "releases-s122-s2.json"))
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var c capturedGrab
			_ = json.Unmarshal(body, &c)
			mu.Lock()
			postCount++
			gotPosts = append(gotPosts, c)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			// 008b — Sonarr's POST /api/v3/release response carries the
			// release object back, optionally with downloadClientId set.
			// Mock the realistic case where it IS present (4242) so the
			// integration test can verify grab_records.download_id is
			// populated end-to-end.
			_, _ = w.Write([]byte(`{"guid":"rt-1","indexerId":1,"downloadClientId":4242}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	db, err := database.Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: filepath.Join(tmp, "test.db")},
	})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	client := sonarr.New("test", srv.URL, "key", 10*time.Second, log)

	decisionRepo := grabpersistence.NewDecisionRepository(db)
	scanRepo := catalogpersistence.NewScanRepository(db)
	grabRepo := grabpersistence.NewGrabRepository(db)
	cdRepo := watchdogpersistence.NewCooldownRepository(db)
	originRepo := enrichpersistence.NewOriginReleaseRepository(db)
	evaluator := evaluate.NewPerInstanceUseCase(decisionRepo, log)
	grabUC := grab.NewUseCase(grabRepo, cdRepo, originRepo, sonarr.Classifier{}, log).
		WithSleeper(func(_ context.Context, _ time.Duration) error { return nil })

	uc := scan.NewUseCase([]scan.Instance{{
		Config: config.SonarrInstance{
			Name: "test",
			Search: config.SearchConfig{
				SkipSpecials: true,
				SkipAnime:    true,
			},
			Limits:  config.LimitsConfig{ScanMaxSeries: 10, MaxGrabsPerScan: 10},
			Ranking: config.RankingConfig{OriginBonus: 1.0},
			Retry:   config.RetryConfig{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
			Cooldown: config.CooldownConfig{
				Mode:                "smart",
				SeriesAfterGrab:     24 * time.Hour,
				GUIDAfterFailedGrab: 72 * time.Hour,
			},
		},
		Client: client,
	}}, evaluator, scanRepo, log, false). // real grab, NOT dry-run
						WithGrabUseCase(grabUC).
						WithCooldowns(cdRepo).
						WithOrigins(originRepo)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := uc.Run(ctx, scan.TriggerManual)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "completed", res[0].Status)
	assert.GreaterOrEqual(t, res[0].Grabs, 1)

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, postCount, 1, "expected at least one POST /release")
	assert.NotEmpty(t, gotPosts[0].GUID)

	var grabCount int64
	require.NoError(t, db.Table("grab_records").Where("status = ?", "grabbed").Count(&grabCount).Error)
	assert.GreaterOrEqual(t, grabCount, int64(1), "expected at least one grabbed row in grab_records")

	// 008b — confirm DownloadID round-trips from Sonarr POST response → UC → repo.
	var downloadIDs []string
	require.NoError(t, db.Table("grab_records").Where("status = ?", "grabbed").Pluck("download_id", &downloadIDs).Error)
	require.NotEmpty(t, downloadIDs, "at least one grabbed row required for download_id pluck")
	foundExpected := false
	for _, v := range downloadIDs {
		if v == "4242" {
			foundExpected = true
			break
		}
	}
	assert.True(t, foundExpected, "expected at least one grabbed row to carry download_id=4242 from the mock response, got: %v", downloadIDs)

	var originCount int64
	require.NoError(t, db.Table("origin_releases").Where("source = ?", "our_grab").Count(&originCount).Error)
	assert.GreaterOrEqual(t, originCount, int64(1), "expected at least one origin_releases row from our_grab")

	c, ok, err := cdRepo.Get(ctx, cooldown.ScopeSeries, cooldown.SeriesKey("test", 122, 2))
	require.NoError(t, err)
	assert.True(t, ok, "series cooldown must be set after successful grab")
	assert.True(t, c.ExpiresAt.After(time.Now().UTC()))
}

func TestIntegration_RealGrab_5xxExhausts_ActivatesGUIDCooldown(t *testing.T) {
	t.Skip("pending D-6 grab+watchdog rewrite — D2-revised-roadmap.md")
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/system/status", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "system-status.json"))
	})
	mux.HandleFunc("/api/v3/series", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[`))
		_, _ = w.Write(loadFixture(t, "series-122-detail.json"))
		_, _ = w.Write([]byte(`]`))
	})
	mux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "episodes-s122-s2.json"))
	})
	mux.HandleFunc("/api/v3/episodeFile", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "episodefile-s122.json"))
	})
	mux.HandleFunc("/api/v3/qualityprofile/14", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "qualityprofile-14.json"))
	})
	mux.HandleFunc("/api/v3/indexer", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "indexer-list.json"))
	})
	mux.HandleFunc("/api/v3/tag", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "tag-list.json"))
	})
	mux.HandleFunc("/api/v3/history", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(loadFixture(t, "history-s122-grabbed.json"))
	})

	var (
		mu        sync.Mutex
		postCount int
	)
	mux.HandleFunc("/api/v3/release", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write(loadFixture(t, "releases-s122-s2.json"))
		case http.MethodPost:
			mu.Lock()
			postCount++
			mu.Unlock()
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	db, err := database.Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: filepath.Join(tmp, "test.db")},
	})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	client := sonarr.New("test", srv.URL, "key", 10*time.Second, log)

	decisionRepo := grabpersistence.NewDecisionRepository(db)
	scanRepo := catalogpersistence.NewScanRepository(db)
	grabRepo := grabpersistence.NewGrabRepository(db)
	cdRepo := watchdogpersistence.NewCooldownRepository(db)
	originRepo := enrichpersistence.NewOriginReleaseRepository(db)
	evaluator := evaluate.NewPerInstanceUseCase(decisionRepo, log)
	grabUC := grab.NewUseCase(grabRepo, cdRepo, originRepo, sonarr.Classifier{}, log).
		WithSleeper(func(_ context.Context, _ time.Duration) error { return nil })

	uc := scan.NewUseCase([]scan.Instance{{
		Config: config.SonarrInstance{
			Name:    "test",
			Search:  config.SearchConfig{SkipSpecials: true, SkipAnime: true},
			Limits:  config.LimitsConfig{ScanMaxSeries: 10, MaxGrabsPerScan: 10},
			Ranking: config.RankingConfig{OriginBonus: 1.0},
			Retry:   config.RetryConfig{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
			Cooldown: config.CooldownConfig{
				Mode:                "smart",
				SeriesAfterGrab:     24 * time.Hour,
				GUIDAfterFailedGrab: 72 * time.Hour,
			},
		},
		Client: client,
	}}, evaluator, scanRepo, log, false).
		WithGrabUseCase(grabUC).
		WithCooldowns(cdRepo).
		WithOrigins(originRepo)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, _ = uc.Run(ctx, scan.TriggerManual)

	mu.Lock()
	assert.Equal(t, 3, postCount, "expected exactly 3 attempts on 5xx (retry budget)")
	mu.Unlock()

	var failedCount int64
	require.NoError(t, db.Table("grab_records").Where("status = ?", "grab_failed").Count(&failedCount).Error)
	assert.GreaterOrEqual(t, failedCount, int64(1), "expected a grab_failed row in grab_records")

	// guid cooldown should now be set for the selected guid.
	var cdCount int64
	require.NoError(t, db.Table("cooldowns").Where("scope = ?", "guid").Count(&cdCount).Error)
	assert.GreaterOrEqual(t, cdCount, int64(1), "expected a guid cooldown row after final failure")
}
