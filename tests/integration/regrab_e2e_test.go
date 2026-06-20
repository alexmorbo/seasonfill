//go:build integration_e2e

package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/config"
	grab "github.com/alexmorbo/seasonfill/internal/grab/app"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	infraregrab "github.com/alexmorbo/seasonfill/internal/watchdog/infrastructure/regrab"
	watchdogpersistence "github.com/alexmorbo/seasonfill/internal/watchdog/persistence"
)

// regrabHarness bundles every collaborator the test needs so the
// orchestration body stays readable. Construction lives in
// newRegrabHarness; teardown is t.Cleanup chained.
type regrabHarness struct {
	uc          *regrab.UseCase
	cooldowns   ports.CooldownRepository
	grabRepo    ports.GrabRepository
	originalID  uuid.UUID
	instanceID  uint
	seriesID    int
	season      int
	hash        string
	sonarrPOSTs *atomic.Int32
}

const (
	testInstanceName = "alpha"
	testSeriesID     = 122
	testSeason       = 2
	testCategory     = "tv-sonarr"
	testHash         = "deadbeefcafebabefeedfacedeadbeefcafebabe"
)

func TestRegrab_E2E_FullCycle_GrabsThenCooldownBlocks(t *testing.T) {
	t.Parallel()
	h := newRegrabHarness(t)

	ctx := context.Background()
	res1, err := h.uc.RunInstance(ctx, testInstanceName)
	require.NoError(t, err)
	assert.Equal(t, 1, res1.UnregisteredCount, "qBit returned one unregistered torrent")
	assert.Equal(t, 1, res1.RegrabbedCount, "replay path succeeded, grab fired")
	assert.Equal(t, 0, res1.SkippedCooldown, "first call: no cooldown yet")
	assert.Equal(t, 0, res1.ErrorCount, "no error branches")

	// Sonarr POST /release counter should be exactly 2: once from the
	// 114 same-GUID replay path, once from runGrab's grab.UseCase.Execute.
	// Sonarr accepts the duplicate — same shape as the manual "Override
	// and add" UI button (story 114).
	require.EqualValues(t, 2, h.sonarrPOSTs.Load(),
		"first RunInstance should fire two Sonarr grab POSTs (replay + runGrab)")

	// Assert a new grab record landed with replay_of_id = original.
	checkReplayPointer(t, h)

	// Assert cooldown is active for the triple.
	checkCooldownActive(t, h, time.Now().UTC())

	// Second call: same torrent still unregistered (the test mux is
	// stateless). Cooldown should block.
	res2, err := h.uc.RunInstance(ctx, testInstanceName)
	require.NoError(t, err)
	assert.Equal(t, 1, res2.UnregisteredCount, "detector still flags unregistered")
	assert.Equal(t, 0, res2.RegrabbedCount, "cooldown gate blocked second grab")
	assert.Equal(t, 1, res2.SkippedCooldown, "skip_cooldown branch was taken")
	require.EqualValues(t, 2, h.sonarrPOSTs.Load(),
		"second RunInstance should NOT fire another Sonarr POST")
}

func newRegrabHarness(t *testing.T) *regrabHarness {
	t.Helper()

	// --- httptest qBit ---
	qbitMux := http.NewServeMux()
	qbitMux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Set-Cookie", "SID=fake; Path=/")
		_, _ = io.WriteString(w, "Ok.")
	})
	qbitMux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{
            "hash": "`+testHash+`",
            "name": "Test S02 Pack",
            "category": "`+testCategory+`",
            "state": "stalledUP",
            "added_on": 1700000000
        }]`)
	})
	qbitMux.HandleFunc("/api/v2/torrents/trackers", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
            {"url": "http://tracker.example.com/announce", "status": 4, "msg": "torrent not registered with this tracker"}
        ]`)
	})
	qbitSrv := httptest.NewServer(qbitMux)
	t.Cleanup(qbitSrv.Close)

	// --- httptest Sonarr ---
	var sonarrPOSTs atomic.Int32
	sonarrMux := http.NewServeMux()
	sonarrMux.HandleFunc("/api/v3/system/status", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"4.0.0.0"}`)
	})
	sonarrMux.HandleFunc("/api/v3/series/122", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
            "id": 122, "title": "Test Series", "monitored": true,
            "qualityProfileId": 14, "tvdbId": 9000,
            "seasons":[{"seasonNumber":2,"monitored":true}]
        }`)
	})
	sonarrMux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[
            {"id":1,"seriesId":122,"seasonNumber":2,"episodeNumber":1,"hasFile":true,"monitored":true,"airDateUtc":"2024-01-01T00:00:00Z"},
            {"id":2,"seriesId":122,"seasonNumber":2,"episodeNumber":2,"hasFile":false,"monitored":true,"airDateUtc":"2024-01-08T00:00:00Z"}
        ]`)
	})
	sonarrMux.HandleFunc("/api/v3/episodeFile", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	})
	sonarrMux.HandleFunc("/api/v3/qualityprofile/14", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
            "id":14,"name":"HD","items":[
                {"quality":{"id":3,"name":"WEBDL-1080p"},"allowed":true}
            ],"cutoff":3
        }`)
	})
	sonarrMux.HandleFunc("/api/v3/indexer", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"id":1,"name":"test-idx","enable":true,"priority":25}]`)
	})
	sonarrMux.HandleFunc("/api/v3/tag", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	})
	sonarrMux.HandleFunc("/api/v3/release", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, `[{
                "guid":"new-release-guid-9999",
                "title":"Test.S02.1080p.WEB-DL.x264-NEW",
                "indexerId":1,"indexer":"test-idx",
                "size":4000000000,
                "qualityWeight":3,
                "quality":{"quality":{"id":3,"name":"WEBDL-1080p"}},
                "mappedSeasonNumber":2,"mappedEpisodeNumbers":[1,2],"seriesId":122,"fullSeason":true,
                "customFormatScore":100
            }]`)
		case http.MethodPost:
			sonarrPOSTs.Add(1)
			_, _ = io.WriteString(w, `{"guid":"new-release-guid-9999","indexerId":1}`)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	sonarrSrv := httptest.NewServer(sonarrMux)
	t.Cleanup(sonarrSrv.Close)

	// --- DB ---
	tmp := t.TempDir()
	db, err := database.Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: filepath.Join(tmp, "regrab-e2e.db")},
	})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	// --- Repos + cipher ---
	cipher, err := crypto.New("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)

	instanceRepo := repositories.NewSonarrInstanceRepository(db)
	qbitSettingsRepo := repositories.NewQbitSettingsRepository(db)
	scanRepo := repositories.NewScanRepository(db)
	decisionRepo := grabpersistence.NewDecisionRepository(db)
	grabRepo := grabpersistence.NewGrabRepository(db)
	cooldownRepo := watchdogpersistence.NewCooldownRepository(db)
	originRepo := repositories.NewOriginReleaseRepository(db)
	blacklistRepo := repositories.NewWatchdogBlacklistRepository(db)
	counterRepo := watchdogpersistence.NewNoBetterCounterRepository(db)

	_ = scanRepo // referenced by use cases that need a scan id source

	// --- Seed instance ---
	instanceSnap := runtime.InstanceSnapshot{
		Name: testInstanceName, URL: sonarrSrv.URL, APIKey: "test-api-key",
		Timeout: 5 * time.Second, SearchTimeout: 5 * time.Second,
		Search: runtime.SearchSnapshot{RequireAllAired: false, SkipSpecials: true},
		Retry: runtime.RetrySnapshot{
			MaxAttempts: 1, InitialBackoff: 100 * time.Millisecond,
			MaxBackoff: time.Second,
		},
		Cooldown: runtime.CooldownSnapshot{
			Mode: "smart", SeriesAfterGrab: time.Hour,
			GUIDAfterFailedGrab: time.Hour, GUIDAfterFailedImport: time.Hour,
		},
		Limits: runtime.LimitsSnapshot{MaxGrabsPerScan: 10},
	}
	runtime.ApplyInstanceDefaults(&instanceSnap)
	instanceID, err := instanceRepo.Create(context.Background(), instanceSnap, cipher)
	require.NoError(t, err)
	instanceSnap.ID = instanceID

	// --- Seed qbit settings ---
	now := time.Now().UTC()
	require.NoError(t, qbitSettingsRepo.Upsert(context.Background(), ports.QbitSettingsRecord{
		InstanceID:             instanceID,
		Enabled:                true,
		URL:                    qbitSrv.URL,
		Category:               testCategory,
		PollIntervalMinutes:    30,
		RegrabCooldownHours:    1,
		MaxConsecutiveNoBetter: 3,
		CreatedAt:              now,
		UpdatedAt:              now,
	}))

	// --- Seed original grab_records row (the one Watchdog will find by hash) ---
	originalID := uuid.New()
	hashLower := strings.ToLower(testHash)
	originalGrab := domaingrab.Record{
		ID:           originalID,
		ScanRunID:    uuid.New(),
		InstanceName: testInstanceName,
		SeriesID:     testSeriesID,
		SeriesTitle:  "Test Series",
		SeasonNumber: testSeason,
		ReleaseGUID:  "original-release-guid",
		ReleaseTitle: "Test.S02.1080p.WEB-DL.x264-OLD",
		IndexerID:    1,
		IndexerName:  "test-idx",
		Quality:      "WEBDL-1080p",
		Status:       domaingrab.StatusGrabbed,
		TorrentHash:  &hashLower,
		CreatedAt:    now.Add(-24 * time.Hour),
	}
	require.NoError(t, grabRepo.Create(context.Background(), originalGrab))

	// --- Sonarr client ---
	sonarrClient := sonarr.NewWithOptions(testInstanceName, sonarrSrv.URL,
		"test-api-key", 5*time.Second, nil, slog.Default())
	instanceMap := map[string]scan.Instance{
		testInstanceName: {Config: instanceSnap, Client: sonarrClient},
	}

	// --- Use case wiring ---
	settingsUC := regrab.NewSettingsUseCase(qbitSettingsRepo, instanceRepo, cipher, slog.Default())
	evaluator := evaluate.NewPerInstanceUseCase(decisionRepo, slog.Default())
	txr := repositories.NewGormTransactor(db)
	grabUC := grab.NewUseCase(grabRepo, cooldownRepo, originRepo, sonarr.Classifier{}, slog.Default()).
		WithTransactor(txr)
	regrabUC := regrab.NewUseCase(
		settingsUC,
		fixedInstanceRegistry{m: instanceMap},
		infraregrab.QbitClientFactoryFunc{},
		infraregrab.DetectorFactoryFunc{},
		grabRepo, cooldownRepo, blacklistRepo, counterRepo,
		evaluator, grabUC,
		slog.Default(),
	)

	return &regrabHarness{
		uc:          regrabUC,
		cooldowns:   cooldownRepo,
		grabRepo:    grabRepo,
		originalID:  originalID,
		instanceID:  instanceID,
		seriesID:    testSeriesID,
		season:      testSeason,
		hash:        hashLower,
		sonarrPOSTs: &sonarrPOSTs,
	}
}

func checkReplayPointer(t *testing.T, h *regrabHarness) {
	t.Helper()
	// Fetch all grab records for the series and find the one with replay_of_id pointing to the original.
	ctx := context.Background()
	instanceName := testInstanceName
	seriesID := h.seriesID
	season := h.season
	allRecords, _, err := h.grabRepo.List(ctx, ports.GrabFilter{
		Instance:     &instanceName,
		SeriesID:     &seriesID,
		SeasonNumber: &season,
	}, ports.Pagination{Limit: 1000})
	require.NoError(t, err, "expected to fetch grab records")
	require.Greater(t, len(allRecords), 1, "expected at least two records")

	var replayRecord *domaingrab.Record
	for i := range allRecords {
		if allRecords[i].ReplayOfID != nil && *allRecords[i].ReplayOfID == h.originalID {
			replayRecord = &allRecords[i]
			break
		}
	}
	require.NotNil(t, replayRecord, "replay grab_record must exist with replay_of_id pointing to original")
	assert.NotEqual(t, h.originalID, replayRecord.ID, "new grab record must have different ID than original")
	assert.Equal(t, h.originalID, *replayRecord.ReplayOfID, "replay_of_id must point at the original")
}

func checkCooldownActive(t *testing.T, h *regrabHarness, now time.Time) {
	t.Helper()
	key := cooldown.SeriesKey(testInstanceName, h.seriesID, h.season)
	// CooldownRepository.Get is the canonical accessor.
	cd, found, err := h.cooldowns.Get(context.Background(), cooldown.ScopeRegrabRetry, key)
	require.NoError(t, err)
	require.True(t, found, "regrab cooldown row must exist")
	assert.True(t, cd.IsActive(now), "regrab cooldown must be active")
}

// --- helpers ---

// fixedInstanceRegistry satisfies application/regrab.InstanceRegistry
// with a fixed map for the duration of the test.
type fixedInstanceRegistry struct{ m map[string]scan.Instance }

func (r fixedInstanceRegistry) Get(name string) (scan.Instance, bool) {
	inst, ok := r.m[name]
	return inst, ok
}

// fmt + json + assert references kept alive in case the
// Implementation Agent inlines some of the placeholder helpers.
var _ = json.Marshal
