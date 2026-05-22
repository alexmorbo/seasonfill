package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	domainwebhook "github.com/alexmorbo/seasonfill/domain/webhook"
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
func (n *noopSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (n *noopSonarr) Name() string { return n.name }

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

type noopWebhookUC struct{}

func (noopWebhookUC) Process(_ context.Context, _ domainwebhook.Event) error {
	panic("fake Process unexpectedly called - this stub is not configured for webhook events")
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
	}, config.WebhookConfig{}, scanUC, noopWebhookUC{}, checker,
		noopScanRepo{}, noopDecRepo{}, noopGrabRepo{}, nil, nil, lg)
}

type okWebhookUC struct{}

func (okWebhookUC) Process(_ context.Context, _ domainwebhook.Event) error { return nil }

func buildServerWithAuth(t *testing.T, adminKey, webhookSecret string) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarrClient := &noopSonarr{name: "main"}
	evalUC := evaluate.NewUseCase(sonarrClient, noopDecRepo{}, lg)
	scanUC := scan.NewUseCase(
		[]scan.Instance{{Config: config.SonarrInstance{Name: "main"}, Client: sonarrClient}},
		evalUC,
		noopScanRepo{},
		lg,
		true,
	)
	checker := healthcheck.New(db, []ports.SonarrClient{sonarrClient})

	return NewServer(config.HTTPConfig{
		Bind:            "127.0.0.1:0",
		ReadTimeout:     time.Second,
		WriteTimeout:    time.Second,
		IdleTimeout:     time.Second,
		ShutdownTimeout: time.Second,
		Auth: config.AuthConfig{
			Enabled:      adminKey != "",
			APIKey:       adminKey,
			CookieSecret: "server-test-cookie-secret",
			SecureCookie: false,
		},
	}, config.WebhookConfig{Secret: webhookSecret}, scanUC, okWebhookUC{}, checker,
		noopScanRepo{}, noopDecRepo{}, noopGrabRepo{}, nil, nil, lg)
}

// TestServer_WebhookAuthIsolation verifies that, when both admin auth and a
// webhook secret are configured with DIFFERENT keys:
//  1. Sonarr's webhook key reaches the webhook endpoint → 200.
//  2. The admin key is REJECTED by the webhook endpoint → 401.
//  3. The webhook key is REJECTED by admin endpoints → 401.
//
// This test would have caught the original bug where the webhook group
// inherited the admin APIKeyAuth middleware (both keys ran against the same
// header, causing a deadlock or privilege-confusion when keys differed).
func TestServer_WebhookAuthIsolation(t *testing.T) {
	const adminKey = "admin-secret"
	const webhookKey = "sonarr-secret"

	srv := buildServerWithAuth(t, adminKey, webhookKey)
	handler := srv.server.Handler

	sonarrPayload := []byte(`{"eventType":"Download","instanceName":"ignored","downloadId":"ABC1","series":{"id":1,"title":"Test"},"episodes":[{"id":1,"seasonNumber":1,"episodeNumber":1}],"episodeFile":{"id":1,"quality":"HDTV-720p"}}`)

	// 1. Webhook key reaches webhook → 200.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/sonarr/main", bytes.NewReader(sonarrPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", webhookKey)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code,
		"sonarr key must reach webhook when auth is isolated")

	// 2. Admin key is rejected by webhook → 401.
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/sonarr/main", bytes.NewReader(sonarrPayload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Api-Key", adminKey)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusUnauthorized, w2.Code,
		"admin key must NOT bypass webhook secret — privilege isolation must hold")

	// 3. Webhook key is rejected by admin endpoint → 401.
	req3 := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/instances", nil)
	req3.Header.Set("X-Api-Key", webhookKey)
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	require.Equal(t, http.StatusUnauthorized, w3.Code,
		"webhook key must NOT reach admin routes")
}

// doLogin posts to /auth/login and returns the resulting handler +
// session cookie. Shared helper for login/logout flow tests.
func doLogin(t *testing.T, adminKey string) (http.Handler, *http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	srv := buildServerWithAuth(t, adminKey, "")
	handler := srv.server.Handler
	body := []byte(`{"api_key":"` + adminKey + `"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var sc *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "seasonfill_session" {
			sc = c
			break
		}
	}
	require.NotNil(t, sc, "login must set the session cookie")
	return handler, sc, w
}

func TestServer_LoginFlow_EndToEnd(t *testing.T) {
	const adminKey = "admin-secret"
	handler, sc, _ := doLogin(t, adminKey)
	// Cookie alone authenticates admin routes (no X-Api-Key).
	getReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/instances", nil)
	getReq.AddCookie(sc)
	getW := httptest.NewRecorder()
	handler.ServeHTTP(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)
}

func TestServer_LogoutFlow(t *testing.T) {
	const adminKey = "admin-secret"
	handler, sc, _ := doLogin(t, adminKey)
	// Logout emits a clearing Set-Cookie (Max-Age <= 0).
	logoutReq := httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
		"/api/v1/auth/session", nil)
	logoutReq.AddCookie(sc)
	logoutW := httptest.NewRecorder()
	handler.ServeHTTP(logoutW, logoutReq)
	require.Equal(t, http.StatusNoContent, logoutW.Code)
	var clearing *http.Cookie
	for _, c := range logoutW.Result().Cookies() {
		if c.Name == "seasonfill_session" {
			clearing = c
			break
		}
	}
	require.NotNil(t, clearing, "logout must emit a clearing Set-Cookie")
	require.Empty(t, clearing.Value)
	require.LessOrEqual(t, clearing.MaxAge, 0)
}

func TestServer_LoginRoute_NotGuarded(t *testing.T) {
	// Login route must NOT be wrapped by RequireAuth. Since 009a M4
	// unified the 401 envelope across middleware + handler, we prove
	// reachability via a positive signal: valid key (no cookie/header)
	// → 200 + Set-Cookie. If middleware intercepted, the request 401s
	// before reaching the handler.
	const adminKey = "admin-secret"
	_, _, w := doLogin(t, adminKey)
	require.NotEmpty(t, w.Header().Get("Set-Cookie"))
	require.Contains(t, w.Header().Get("Set-Cookie"), "seasonfill_session=")
}

func TestServer_HeaderAuthBackwardCompat(t *testing.T) {
	const adminKey = "admin-secret"
	srv := buildServerWithAuth(t, adminKey, "")
	// CLI / automation contract: X-Api-Key header alone authenticates.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/instances", nil)
	req.Header.Set("X-Api-Key", adminKey)
	w := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
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
