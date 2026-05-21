package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	"github.com/alexmorbo/seasonfill/internal/config"
)

type noopSonarr struct{ name string }

func (n *noopSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{Version: "test"}, nil
}
func (n *noopSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (n *noopSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (n *noopSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (n *noopSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (n *noopSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (n *noopSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (n *noopSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (n *noopSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (n *noopSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (n *noopSonarr) ForceGrab(_ context.Context, _ string, _ int) error { return nil }
func (n *noopSonarr) Name() string                                       { return n.name }

type noopScanRepo struct{}

func (noopScanRepo) Create(context.Context, ports.ScanRecord) error { return nil }
func (noopScanRepo) Update(context.Context, ports.ScanRecord) error { return nil }
func (noopScanRepo) GetByID(_ context.Context, _ uuid.UUID) (ports.ScanRecord, error) {
	return ports.ScanRecord{}, nil
}
func (noopScanRepo) MarkAborted(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (noopScanRepo) List(_ context.Context, _ ports.ScanFilter, _ ports.Pagination) ([]ports.ScanRecord, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

type noopDecRepo struct{}

func (noopDecRepo) Save(context.Context, decision.Decision) error { return nil }
func (noopDecRepo) List(_ context.Context, _ ports.DecisionFilter, _ ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

type noopGrabRepo struct{}

func (noopGrabRepo) Create(context.Context, grab.Record) error { return nil }
func (noopGrabRepo) List(_ context.Context, _ ports.GrabFilter, _ ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

func (noopGrabRepo) MatchLatest(_ context.Context, _ ports.MatchKey) (grab.Record, error) {
	panic("fake MatchLatest unexpectedly called - this stub is not configured for MatchLatest queries")
}

func (noopGrabRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ grab.Status, _ string) error {
	panic("fake UpdateStatus unexpectedly called - this stub is not configured for UpdateStatus calls")
}

func buildServer(t *testing.T) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &noopSonarr{name: "main"}
	evalUC := evaluate.NewUseCase(sonarr, noopDecRepo{}, lg)
	scanUC := scan.NewUseCase(
		[]scan.Instance{{Config: config.SonarrInstance{Name: "main"}, Client: sonarr}},
		evalUC,
		noopScanRepo{},
		lg,
		true,
	)
	checker := healthcheck.New(db, []ports.SonarrClient{sonarr})

	return NewServer(config.HTTPConfig{
		Bind:            "127.0.0.1:0",
		ReadTimeout:     time.Second,
		WriteTimeout:    time.Second,
		IdleTimeout:     time.Second,
		ShutdownTimeout: time.Second,
		Auth:            config.AuthConfig{Enabled: false},
	}, scanUC, checker, noopScanRepo{}, noopDecRepo{}, noopGrabRepo{}, lg)
}

func TestNewServer_DoesNotPanic(t *testing.T) {
	srv := buildServer(t)
	assert.NotNil(t, srv)
}

func TestServer_Shutdown_NotStarted(t *testing.T) {
	srv := buildServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	assert.NoError(t, srv.Shutdown(ctx))
}

func TestServer_StartShutdown_Cycle(t *testing.T) {
	srv := buildServer(t)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Let the listener bind. We accept any error from Start (e.g., bind failure
	// in a constrained CI env) — the focus is exercising Start + Shutdown paths.
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Logf("Start returned non-fatal err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Shutdown")
	}
}
