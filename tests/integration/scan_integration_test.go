//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "infrastructure", "sonarr", "fixtures", name)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func TestIntegration_ScanHijackSeason2_DryRun_LogsGrabDecision(t *testing.T) {
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
	mux.HandleFunc("/api/v3/release", func(w http.ResponseWriter, r *http.Request) {
		// In dry-run, only GET is expected. Any POST is a contract violation.
		if r.Method != http.MethodGet {
			t.Fatalf("dry-run violation: %s /release", r.Method)
		}
		_, _ = w.Write(loadFixture(t, "releases-s122-s2.json"))
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
	scanRepo := repositories.NewScanRepository(db)
	evaluator := evaluate.NewPerInstanceUseCase(decisionRepo, log)

	// Explicit dry_run: true to preserve Phase 1 no-POST contract (Q5).
	uc := scan.NewUseCase([]scan.Instance{{
		Config: config.SonarrInstance{
			Name: "test",
			Search: config.SearchConfig{
				SkipSpecials: true,
				SkipAnime:    true,
			},
			Limits:  config.LimitsConfig{ScanMaxSeries: 10},
			Ranking: config.RankingConfig{OriginBonus: 1.0},
		},
		Client: client,
	}}, evaluator, scanRepo, log, true)

	_ = ports.SonarrClient(client)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := uc.Run(ctx, scan.TriggerManual)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "completed", res[0].Status)
	assert.GreaterOrEqual(t, res[0].Series, 1)
	assert.GreaterOrEqual(t, res[0].Candidates, 1)
}
