package edge

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	domainwebhook "github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type noopSonarr struct{ name string }

func (n *noopSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{Version: "test"}, nil
}
func (n *noopSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (n *noopSonarr) ListSeriesCache(_ context.Context, _ domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (n *noopSonarr) GetSeries(_ context.Context, _ domain.SonarrSeriesID) (series.Series, error) {
	return series.Series{}, nil
}
func (n *noopSonarr) ListEpisodes(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]series.Episode, error) {
	return nil, nil
}

func (n *noopSonarr) ListEpisodesBySeries(_ context.Context, _ domain.SonarrSeriesID) ([]series.Episode, error) {
	return nil, nil
}
func (n *noopSonarr) ListEpisodeFiles(_ context.Context, _ domain.SonarrSeriesID) (map[int]int, error) {
	return nil, nil
}
func (n *noopSonarr) ListEpisodeFilesBySeason(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
}
func (n *noopSonarr) SearchReleases(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]release.Release, error) {
	return nil, nil
}
func (n *noopSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (n *noopSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (n *noopSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (n *noopSonarr) ListQualityProfiles(_ context.Context) ([]ports.QualityProfile, error) {
	return nil, nil
}
func (n *noopSonarr) ListRootFolders(_ context.Context) ([]ports.RootFolder, error) { return nil, nil }
func (n *noopSonarr) LookupSeries(_ context.Context, _ string) ([]ports.SonarrLookupResult, error) {
	return nil, nil
}
func (n *noopSonarr) CreateTag(_ context.Context, _ string) (ports.Tag, error) {
	return ports.Tag{}, nil
}
func (n *noopSonarr) AddSeries(_ context.Context, _ ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
	return ports.AddSeriesResult{}, nil
}
func (n *noopSonarr) GrabHistory(_ context.Context, _ domain.SonarrSeriesID) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (n *noopSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (n *noopSonarr) ParseRelease(_ context.Context, _ string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
}
func (n *noopSonarr) Name() string { return n.name }

type noopScanRepo struct{}

func (noopScanRepo) Create(context.Context, ports.ScanRecord) error { return nil }
func (noopScanRepo) Update(context.Context, ports.ScanRecord) error { return nil }
func (noopScanRepo) GetByID(_ context.Context, _ uuid.UUID) (ports.ScanRecord, error) {
	return ports.ScanRecord{}, nil
}
func (noopScanRepo) MarkAborted(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (noopScanRepo) IncrementSeriesScanned(_ context.Context, _ uuid.UUID, _ int) error {
	return nil
}
func (noopScanRepo) List(_ context.Context, _ ports.ScanFilter, _ ports.Pagination) ([]ports.ScanRecord, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

type noopDecRepo struct{}

func (noopDecRepo) Save(context.Context, decision.Decision) error { return nil }
func (noopDecRepo) GetByID(context.Context, uuid.UUID) (decision.Decision, error) {
	return decision.Decision{}, ports.ErrNotFound
}
func (noopDecRepo) List(_ context.Context, _ ports.DecisionFilter, _ ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

func (noopDecRepo) UpdateSupersededBy(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (noopDecRepo) ClearSupersededBy(context.Context, uuid.UUID) error { return nil }

func (noopDecRepo) UpdateIntent(context.Context, uuid.UUID, *decision.Intent) error {
	return nil
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

func (noopGrabRepo) UpdateTorrentHash(_ context.Context, _ uuid.UUID, _ string) error {
	panic("fake UpdateTorrentHash unexpectedly called - this stub is not configured for UpdateTorrentHash calls")
}

func (noopGrabRepo) FindLatestSuccessByHash(_ context.Context, _ string) (grab.Record, error) {
	panic("fake FindLatestSuccessByHash unexpectedly called - this stub is not configured")
}

func (noopGrabRepo) CreateReplay(_ context.Context, _ grab.Record, _ uuid.UUID) error {
	panic("fake CreateReplay unexpectedly called - this stub is not configured")
}

func (noopGrabRepo) SetReplayOfID(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	panic("fake SetReplayOfID unexpectedly called - this stub is not configured")
}

func (noopGrabRepo) ListReplaysOf(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return map[uuid.UUID][]uuid.UUID{}, nil
}

func (noopGrabRepo) UpdateSizeBytes(_ context.Context, _ uuid.UUID, _ int64) error {
	panic("fake UpdateSizeBytes unexpectedly called - this stub is not configured")
}

func (noopGrabRepo) GetByID(_ context.Context, _ uuid.UUID) (grab.Record, error) {
	panic("fake GetByID unexpectedly called - this stub is not configured")
}

func (noopGrabRepo) CountReplaysSince(_ context.Context, _ domain.InstanceName, _ time.Time) (int, error) {
	return 0, nil
}

func (noopGrabRepo) CountReplaysAll(_ context.Context, _ domain.InstanceName) (int, error) {
	return 0, nil
}

func (noopGrabRepo) CountImportedEpisodes(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID, _ int) (int, error) {
	return 0, nil
}
func (noopGrabRepo) ListUnparsedSince(_ context.Context, _ time.Time, _ int) ([]grab.Record, error) {
	return nil, nil
}
func (noopGrabRepo) UpdateParsed(_ context.Context, _ uuid.UUID, _ *grab.Parsed, _ time.Time) error {
	return nil
}

type noopWebhookUC struct{}

func (noopWebhookUC) Process(_ context.Context, _ domainwebhook.Event) error {
	panic("fake Process unexpectedly called - this stub is not configured for webhook events")
}

// stubAdminRepo is the http package's local fake. The handlers package
// has its own copy (different package); duplicating 25 lines beats
// adding a shared testutil dependency.
type stubAdminRepo struct {
	mu   sync.Mutex
	user *admin.User
}

func (r *stubAdminRepo) Get(_ context.Context) (admin.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user == nil {
		return admin.User{}, ports.ErrNotFound
	}
	return *r.user, nil
}

func (r *stubAdminRepo) Create(_ context.Context, u admin.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.user = &u
	return nil
}

func (r *stubAdminRepo) UpdatePassword(_ context.Context, _ uint, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user == nil {
		return ports.ErrNotFound
	}
	r.user.PasswordHash = hash
	r.user.AutoGenerated = false
	return nil
}
func (r *stubAdminRepo) UpdateLastLoginAt(_ context.Context, _ uint, when time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user == nil {
		return ports.ErrNotFound
	}
	t := when
	r.user.LastLoginAt = &t
	return nil
}
func (r *stubAdminRepo) GetByOIDCSubject(_ context.Context, _ string) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (r *stubAdminRepo) CreateFromOIDC(_ context.Context, subject, username, email string) (admin.User, error) {
	sub := subject
	u := admin.User{Username: username, OIDCSubject: &sub}
	if email != "" {
		e := email
		u.Email = &e
	}
	r.mu.Lock()
	r.user = &u
	r.mu.Unlock()
	return u, nil
}
func (r *stubAdminRepo) GetByUsername(_ context.Context, name string) (admin.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user != nil && r.user.Username == name {
		return *r.user, nil
	}
	return admin.User{}, ports.ErrNotFound
}
func (r *stubAdminRepo) UpdateSettings(_ context.Context, _ uint, patch ports.UserSettingsPatch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user == nil {
		return ports.ErrNotFound
	}
	if patch.AvatarMode != nil {
		r.user.AvatarMode = *patch.AvatarMode
	}
	if patch.PreferredLanguage != nil {
		v := *patch.PreferredLanguage
		r.user.PreferredLanguage = &v
	}
	return nil
}

func buildServer(t *testing.T) *Server {
	t.Helper()

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
		Auth:            config.AuthConfig{Enabled: false},
	}, scanUC, noopWebhookUC{}, checker,
		noopScanRepo{}, noopDecRepo{}, noopGrabRepo{},
		&stubAdminRepo{}, nil, nil,
		catalogrest.InstanceRegistry{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, // cooldown, grab, rescan, instanceCRUD, instanceProbe, runtimeConfig, qbitSettings, externalServices, oidcUC, webhookReconciler, webhookStatusCache
		nil, nil, // seriesCacheRepo, counterRepo
		nil, nil, nil, nil, // watchdogRollupHandler, watchdogBlacklistHandler, watchdogSeasonsHandler, webhooksAggregateHandler
		nil,           // mediaHandler (Story 214 F-1)
		nil,           // mediaPending (Story 352, nil-OK)
		nil, nil, nil, // seriesDetailHandler + seriesSeasonHandler (Story 215 G-1) + seriesCastHandler (Story 216 H-1)
		nil, // peopleHandler (Story 217 H-2)
		nil, // seriesRefreshHandler (Story 218 E-2)
		nil, // seriesTorrentsHandler (Story 222 A-4)
		nil, // timezoneHandler (Story 301)
		nil, // meHandler (Story 485 N-7a)
		nil, // sharedAuthRuntime (Story 485 N-7a)
		nil, // globalSeriesHandler (Story 491 N-1a)
		nil, // globalOverviewHandler (Story 529)
		nil, // globalRecommendationsHandler (Story 530)
		nil, // globalLibraryHandler (Story 577 E-1-B2)
		nil, // discoveryHandler (Story 507 N-2f)
		nil, // discoverHandler (Story 509 N-2h)
		nil, // instanceMetadataHandler (Story 519 N-4b)
		nil, // addToSonarrHandler (Story 520 N-4c)
		nil, // etagFreshness (Story 578 E-1-B5) — nil-OK pass-through
		lg)
}

type okWebhookUC struct{}

func (okWebhookUC) Process(_ context.Context, _ domainwebhook.Event) error { return nil }

func buildServerWithAuth(t *testing.T, adminKey string) *Server {
	t.Helper()

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

	hash, err := authapp.HashPassword("secret-pw")
	require.NoError(t, err)
	adminRepo := &stubAdminRepo{user: &admin.User{
		ID: 1, Username: "admin", PasswordHash: hash,
	}}

	return NewServer(config.HTTPConfig{
		Bind:            "127.0.0.1:0",
		ReadTimeout:     time.Second,
		WriteTimeout:    time.Second,
		IdleTimeout:     time.Second,
		ShutdownTimeout: time.Second,
		Auth: config.AuthConfig{
			Enabled:      adminKey != "",
			APIKey:       adminKey,
			SessionTTL:   time.Hour,
			WebUser:      "admin",
			SecureCookie: false,
		},
	}, scanUC, okWebhookUC{}, checker,
		noopScanRepo{}, noopDecRepo{}, noopGrabRepo{},
		adminRepo, nil, nil,
		catalogrest.InstanceRegistry{Load: func() map[string]scan.Instance {
			return map[string]scan.Instance{"main": {Config: config.SonarrInstance{Name: "main"}}}
		}},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, // cooldown, grab, rescan, instanceCRUD, instanceProbe, runtimeConfig, qbitSettings, externalServices, oidcUC, webhookReconciler, webhookStatusCache
		nil, nil, // seriesCacheRepo, counterRepo
		nil, nil, nil, nil, // watchdogRollupHandler, watchdogBlacklistHandler, watchdogSeasonsHandler, webhooksAggregateHandler
		nil,           // mediaHandler (Story 214 F-1)
		nil,           // mediaPending (Story 352, nil-OK)
		nil, nil, nil, // seriesDetailHandler + seriesSeasonHandler (Story 215 G-1) + seriesCastHandler (Story 216 H-1)
		nil, // peopleHandler (Story 217 H-2)
		nil, // seriesRefreshHandler (Story 218 E-2)
		nil, // seriesTorrentsHandler (Story 222 A-4)
		nil, // timezoneHandler (Story 301)
		nil, // meHandler (Story 485 N-7a)
		nil, // sharedAuthRuntime (Story 485 N-7a)
		nil, // globalSeriesHandler (Story 491 N-1a)
		nil, // globalOverviewHandler (Story 529)
		nil, // globalRecommendationsHandler (Story 530)
		nil, // globalLibraryHandler (Story 577 E-1-B2)
		nil, // discoveryHandler (Story 507 N-2f)
		nil, // discoverHandler (Story 509 N-2h)
		nil, // instanceMetadataHandler (Story 519 N-4b)
		nil, // addToSonarrHandler (Story 520 N-4c)
		nil, // etagFreshness (Story 578 E-1-B5) — nil-OK pass-through
		lg)
}

func TestServer_WebhookRequiresAuth(t *testing.T) {
	t.Parallel()
	srv := buildServerWithAuth(t, "apikey-xyz")

	// No auth → 401.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/sonarr/main", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	srv.engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// X-Api-Key → not 401 (200 or 400 depending on payload; the
	// assertion is auth passed).
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/sonarr/main",
		bytes.NewReader([]byte(`{"eventType":"Test"}`)))
	req.Header.Set("X-Api-Key", "apikey-xyz")
	w = httptest.NewRecorder()
	srv.engine.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code)
}

// doLogin posts to /auth/login and returns the resulting handler +
// session cookie. Shared helper for login/logout flow tests.
func doLogin(t *testing.T, adminKey string) (http.Handler, *http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	srv := buildServerWithAuth(t, adminKey)
	handler := srv.server.Handler
	body := []byte(`{"username":"admin","password":"secret-pw"}`)
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
	// Story 492 / N-1b — instance list moved under /admin/instances.
	getReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/admin/instances", nil)
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
	const adminKey = "admin-secret"
	_, _, w := doLogin(t, adminKey)
	require.NotEmpty(t, w.Header().Get("Set-Cookie"))
	require.Contains(t, w.Header().Get("Set-Cookie"), "seasonfill_session=")
}

func TestServer_HeaderAuthBackwardCompat(t *testing.T) {
	const adminKey = "admin-secret"
	srv := buildServerWithAuth(t, adminKey)
	// CLI / automation contract: X-Api-Key header alone authenticates.
	// Story 492 / N-1b — instance list moved under /admin/instances.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/admin/instances", nil)
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

// TestNewServer_TrustedProxies_Honored covers HIGH-S2: the
// constructor must forward Auth.TrustedProxies to the underlying gin
// engine. With no trusted proxies, c.ClientIP() falls back to
// RemoteAddr — X-Forwarded-For is ignored. We probe via a tiny
// route registered on the same engine instance.
func TestNewServer_TrustedProxies_Honored(t *testing.T) {
	srv := buildServer(t) // Auth.Enabled=false, TrustedProxies=nil
	// Empty trusted-proxies list ⇒ RemoteAddr only.
	srv.engine.GET("/__client_ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/__client_ip", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	w := httptest.NewRecorder()
	srv.engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	// XFF MUST be ignored because the test server runs with no trusted
	// proxies. The reported client IP is the RemoteAddr host.
	assert.Equal(t, "192.0.2.10", w.Body.String())
}

// TestNewServer_TrustedProxies_HonorsLocalhost covers HIGH-S2 with the
// default ["127.0.0.1", "::1"] list. A request originating from
// localhost gets its XFF honored; a request from a non-trusted IP
// does not.
func TestNewServer_TrustedProxies_HonorsLocalhost(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarrClient := &noopSonarr{name: "main"}
	evalUC := evaluate.NewUseCase(sonarrClient, noopDecRepo{}, lg)
	scanUC := scan.NewUseCase(
		[]scan.Instance{{Config: config.SonarrInstance{Name: "main"}, Client: sonarrClient}},
		evalUC, noopScanRepo{}, lg, true,
	)
	checker := healthcheck.New(db, []ports.SonarrClient{sonarrClient})

	srv := NewServer(config.HTTPConfig{
		Bind: "127.0.0.1:0",
		Auth: config.AuthConfig{
			Enabled:        false,
			TrustedProxies: []string{"127.0.0.1", "::1"},
		},
	}, scanUC, noopWebhookUC{}, checker,
		noopScanRepo{}, noopDecRepo{}, noopGrabRepo{},
		&stubAdminRepo{}, nil, nil,
		catalogrest.InstanceRegistry{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, // cooldown, grab, rescan, instanceCRUD, instanceProbe, runtimeConfig, qbitSettings, externalServices, oidcUC, webhookReconciler, webhookStatusCache
		nil, nil, // seriesCacheRepo, counterRepo
		nil, nil, nil, nil, // watchdogRollupHandler, watchdogBlacklistHandler, watchdogSeasonsHandler, webhooksAggregateHandler
		nil,           // mediaHandler (Story 214 F-1)
		nil,           // mediaPending (Story 352, nil-OK)
		nil, nil, nil, // seriesDetailHandler + seriesSeasonHandler (Story 215 G-1) + seriesCastHandler (Story 216 H-1)
		nil, // peopleHandler (Story 217 H-2)
		nil, // seriesRefreshHandler (Story 218 E-2)
		nil, // seriesTorrentsHandler (Story 222 A-4)
		nil, // timezoneHandler (Story 301)
		nil, // meHandler (Story 485 N-7a)
		nil, // sharedAuthRuntime (Story 485 N-7a)
		nil, // globalSeriesHandler (Story 491 N-1a)
		nil, // globalOverviewHandler (Story 529)
		nil, // globalRecommendationsHandler (Story 530)
		nil, // globalLibraryHandler (Story 577 E-1-B2)
		nil, // discoveryHandler (Story 507 N-2f)
		nil, // discoverHandler (Story 509 N-2h)
		nil, // instanceMetadataHandler (Story 519 N-4b)
		nil, // addToSonarrHandler (Story 520 N-4c)
		nil, // etagFreshness (Story 578 E-1-B5) — nil-OK pass-through
		lg)

	srv.engine.GET("/__client_ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})

	// From localhost (trusted) — XFF is honored.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/__client_ip", nil)
	req.RemoteAddr = "127.0.0.1:55555"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	w := httptest.NewRecorder()
	srv.engine.ServeHTTP(w, req)
	assert.Equal(t, "203.0.113.99", w.Body.String())

	// From a non-trusted IP — XFF ignored, falls back to RemoteAddr.
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/__client_ip", nil)
	req2.RemoteAddr = "198.51.100.7:55555"
	req2.Header.Set("X-Forwarded-For", "203.0.113.99")
	w2 := httptest.NewRecorder()
	srv.engine.ServeHTTP(w2, req2)
	assert.Equal(t, "198.51.100.7", w2.Body.String())
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

// TestNewServer_LegacyRoutesReturn404 — Story 492 / N-1b. Verifies the
// per-instance series-scoped surfaces and the per-instance instance
// CRUD/list endpoints are GONE — every legacy path returns 404. The
// webhook receiver `/webhook/sonarr/:instance_name` MUST stay alive
// (PRD §4825 — Sonarr-facing endpoint unchanged).
func TestNewServer_LegacyRoutesReturn404(t *testing.T) {
	t.Parallel()
	const adminKey = "admin-secret"
	srv := buildServerWithAuth(t, adminKey)
	handler := srv.server.Handler

	legacyGETPaths := []string{
		"/api/v1/instances",
		"/api/v1/instances/main",
		"/api/v1/instances/main/missing",
		"/api/v1/instances/main/counters",
		"/api/v1/instances/main/series-cache",
		"/api/v1/instances/main/series-cache/networks",
		"/api/v1/instances/main/series",
		"/api/v1/instances/main/series/140",
		"/api/v1/instances/main/series/140/season/1",
		"/api/v1/instances/main/series/140/cast",
		"/api/v1/instances/main/series/140/torrents",
		"/api/v1/instances/main/series/140/seasons/1/episodes",
		"/api/v1/instances/main/grabs/" + uuid.New().String() + "/episode-files",
	}
	for _, path := range legacyGETPaths {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.Header.Set("X-Api-Key", adminKey)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusNotFound, w.Code, "legacy GET %s must be 404 after N-1b delete", path)
	}

	// The per-instance series/:id/refresh POST is also gone (replaced
	// by the global /series/:id/regrab in 491).
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/instances/main/series/140/refresh", nil)
	req.Header.Set("X-Api-Key", adminKey)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code, "legacy POST /series/:id/refresh must be 404")

	// Webhook receiver MUST stay alive — Sonarr posts to it (PRD §4825).
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/sonarr/main", bytes.NewReader([]byte(`{"eventType":"Test"}`)))
	req.Header.Set("X-Api-Key", adminKey)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.NotEqualf(t, http.StatusNotFound, w.Code,
		"webhook receiver must stay registered (got %d %s)", w.Code, w.Body.String())
}

// TestNewServer_AdminInstancesRouteRegistered — Story 492 / N-1b.
// Verifies the renamed admin instance surface answers (NOT 404).
func TestNewServer_AdminInstancesRouteRegistered(t *testing.T) {
	t.Parallel()
	const adminKey = "admin-secret"
	srv := buildServerWithAuth(t, adminKey)
	handler := srv.server.Handler

	for _, path := range []string{
		"/api/v1/admin/instances",
		"/api/v1/admin/instances/main",
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.Header.Set("X-Api-Key", adminKey)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.NotEqualf(t, http.StatusNotFound, w.Code,
			"admin path %s must be registered (got %d)", path, w.Code)
	}
}

// TestNewServer_GlobalSeriesScopedRoutesRegistered — Story 492 / N-1b.
// Verifies the new global routes that are unconditionally registered
// (season-episodes + grab episode-files) hit a wrapper handler, NOT
// gin's default 404 page. The cast / season / torrents wrappers use
// the same nil-OK pattern as their inner per-instance handlers — they
// register only when the inner is wired, and the buildServerWithAuth
// rig passes nil for the inners. Those routes are NOT asserted here
// (covered by the live-curl smoke step in the story's Verify plan).
func TestNewServer_GlobalSeriesScopedRoutesRegistered(t *testing.T) {
	t.Parallel()
	const adminKey = "admin-secret"
	srv := buildServerWithAuth(t, adminKey)
	handler := srv.server.Handler

	for _, path := range []string{
		"/api/v1/series/140/seasons/1/episodes",
		"/api/v1/grabs/" + uuid.New().String() + "/episode-files",
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.Header.Set("X-Api-Key", adminKey)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		// A registered wrapper handler always emits a JSON envelope —
		// gin's default plain-text 404 body MUST NOT appear.
		assert.NotContainsf(t, w.Body.String(), "404 page not found",
			"global path %s must hit a wrapper handler (gin default 404 body observed)", path)
		assert.NotEqualf(t, http.StatusNotFound, w.Code,
			"global path %s must be registered (route-level 404 means not registered)", path)
	}
}
