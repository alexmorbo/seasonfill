package edge

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	adminrest "github.com/alexmorbo/seasonfill/internal/admin/rest"
	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	apprescan "github.com/alexmorbo/seasonfill/internal/catalog/app/rescan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/webhookinstall"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/config"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	enrichrest "github.com/alexmorbo/seasonfill/internal/enrichment/rest"
	appgrab "github.com/alexmorbo/seasonfill/internal/grab/app"
	grabrest "github.com/alexmorbo/seasonfill/internal/grab/rest"
	mediaproxyrest "github.com/alexmorbo/seasonfill/internal/mediaproxy/rest"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	watchdogrest "github.com/alexmorbo/seasonfill/internal/watchdog/rest"
)

type Server struct {
	cfg         config.HTTPConfig
	server      *http.Server
	engine      *gin.Engine
	authHandler *adminrest.AuthHandler
	logger      *slog.Logger
}

func NewServer(
	cfg config.HTTPConfig,
	scanUC *scan.UseCase,
	webhookUC catalogrest.WebhookProcessor,
	checker *healthcheck.Checker,
	scanRepo ports.ScanRepository,
	decisionRepo ports.DecisionRepository,
	grabRepo ports.GrabRepository,
	adminRepo ports.UserRepository,
	loginLimiter *auth.IPLimiter,
	webhookLimiter *auth.IPLimiter,
	instanceReg catalogrest.InstanceRegistry,
	cooldownRepo ports.CooldownRepository,
	grabUC *appgrab.UseCase,
	rescanUC *apprescan.UseCase,
	instanceCRUD *catalogrest.InstanceCRUDHandler,
	instanceProbe *catalogrest.InstanceProbeHandler,
	runtimeConfigHandler *catalogrest.RuntimeConfigHandler,
	qbitSettings *handlers.QbitSettingsHandler,
	externalServices *enrichrest.ExternalServicesHandler,
	oidcUC *auth.OIDCLoginUseCase,
	webhookReconciler *webhookinstall.Reconciler,
	webhookStatusCache *webhookinstall.StatusCache,
	seriesCacheRepo ports.SeriesCacheRepository,
	counterRepo ports.CounterRepository,
	watchdogRollupHandler *watchdogrest.WatchdogRollupHandler,
	watchdogBlacklistHandler *watchdogrest.WatchdogBlacklistHandler,
	watchdogSeasonsHandler *watchdogrest.WatchdogSeasonsHandler,
	webhooksAggregateHandler *catalogrest.WebhooksAggregateHandler,
	mediaHandler *mediaproxyrest.MediaHandler,
	mediaPending adminrest.CatalogMediaPendingWriter,
	peopleHandler *enrichrest.PeopleHandler,
	seriesRefreshHandler *enrichrest.SeriesRefreshHandler,
	seriesTorrentsHandler *seriesdetailrest.SeriesTorrentsHandler,
	timezoneHandler *adminrest.TimezoneHandler,
	meHandler *adminrest.MeHandler,
	sharedAuthRuntime *middleware.AuthRuntimePointer,
	globalSeriesHandler *seriesdetailrest.GlobalSeriesHandler,
	globalCastHandler *seriesdetailrest.GlobalSeriesCastHandler, // story 535
	globalSeasonHandler *seriesdetailrest.GlobalSeriesSeasonHandler, // TMDB-only season fallback
	globalOverviewHandler *seriesdetailrest.GlobalSeriesOverviewHandler, // story 529
	globalRecommendationsHandler *seriesdetailrest.GlobalSeriesRecommendationsHandler, // story 530
	globalLibraryHandler *seriesdetailrest.GlobalSeriesLibraryHandler, // story 577 E-1-B2
	seasonsHandler *seriesdetailrest.SeasonsHandler, // story 582 E-1 B3c
	discoveryHandler *discoveryrest.DiscoveryHandler,
	discoverHandler *discoveryrest.DiscoverHandler, // story 509 N-2h
	instanceMetadataHandler *adminrest.InstanceMetadataHandler, // story 519 N-4b
	addToSonarrHandler *discoveryrest.AddToSonarrHandler, // story 520 N-4c
	// Story 578 / E-1-B5 — per-section freshness reader for the ETag
	// middleware. nil-OK: when nil the middleware is a pass-through, so
	// minimal/test wirings keep working with zero behaviour change.
	etagFreshness SectionSyncedAtReader,
	// Story E-1-B7 — optional series-title localizer for the global
	// series-cache list (?lang=). nil-OK: pass-through, canon titles.
	seriesTitleLocalizer catalogrest.SeriesTextLocalizer,
	// Story 584b — optional per-language poster localizer for the global
	// series-cache list (?lang=). nil-OK: pass-through, canon poster_hash.
	seriesMediaLocalizer catalogrest.SeriesMediaLocalizer,
	logger *slog.Logger,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLoggerMiddleware(logger))
	r.Use(middleware.ErrorResponseMiddleware(logger))

	// HIGH-S2: bound which proxies' X-Forwarded-For we honor. Default
	// (set by config.Defaults) is ["127.0.0.1", "::1"] — only localhost
	// trusted. Empty list disables XFF entirely; SetTrustedProxies(nil)
	// makes c.ClientIP() fall back to RemoteAddr. The login + webhook
	// rate limits rely on this for accurate keying.
	if err := r.SetTrustedProxies(cfg.Auth.TrustedProxies); err != nil {
		logger.Warn("http.trusted_proxies invalid — falling back to RemoteAddr",
			slog.String("error", err.Error()))
		_ = r.SetTrustedProxies(nil)
	}

	healthHandler := adminrest.NewHealthHandler(checker)
	scanHandler := catalogrest.NewScanHandler(scanUC, logger)
	// Singleton episodes cache shared by the Missing handler. Lives
	// for the life of the process — like the poster cache, the cap +
	// TTL are package-level constants (see internal/runtime/snapshot.go).
	// 5-min TTL means operator-driven /missing polls reflect new
	// imports within the next refetch; 32 MiB byte cap holds ~200k
	// episodes worth of metadata.
	episodesCache := sonarr.NewLRUEpisodesCache(
		runtime.EpisodesCacheMaxBytes, runtime.EpisodesCacheTTL)
	instancesHandler := catalogrest.NewInstancesHandler(checker, instanceReg, logger).
		WithSeriesCache(seriesCacheRepo).
		WithEpisodesCache(episodesCache).
		WithMediaPending(mediaPending).
		WithLocalizer(seriesTitleLocalizer).
		WithMediaLocalizer(seriesMediaLocalizer)
	// Story 491 / N-1a — global catalog handler over the per-instance one.
	globalCatalogHandler := catalogrest.NewGlobalCatalogHandler(instancesHandler, logger)
	// Story 492 / N-1b — global series-scoped wrappers + global grab
	// episode-files. Constructed as thin delegates over the per-instance
	// handlers; nil-OK pattern mirrors the per-instance variants so the
	// route is omitted (not 5xx-stubbed) when the inner is absent.
	// Story 535 — globalCastHandler now built in wiring.BuildSeriesDetail
	// so it shares scope with tmdbFallbackUC; passed in as a param. The
	// global season handler moved to wiring for the same reason (TMDB-only
	// season fallback needs tmdbFallbackUC).
	var globalTorrentsHandler *seriesdetailrest.GlobalSeriesTorrentsHandler
	if seriesTorrentsHandler != nil {
		globalTorrentsHandler = seriesdetailrest.NewGlobalSeriesTorrentsHandler(seriesTorrentsHandler, seriesCacheRepo, logger)
	}
	globalSeasonEpisodesHandler := catalogrest.NewGlobalSeasonEpisodesHandler(instancesHandler, seriesCacheRepo, logger)
	globalGrabEpisodeFilesHandler := grabrest.NewGlobalGrabEpisodeFilesHandler(grabRepo, instanceReg, logger)
	auditHandler := handlers.NewAuditHandler(scanRepo, decisionRepo, grabRepo, logger).
		WithSeriesCache(seriesCacheRepo).
		WithMediaPending(mediaPending).
		WithLocalizer(seriesTitleLocalizer)
	webhookHandler := catalogrest.NewWebhookHandler(webhookUC, instanceReg, logger)
	grabHandler := grabrest.NewGrabHandler(decisionRepo, grabRepo, cooldownRepo, grabUC, instanceReg, logger)

	r.GET("/healthz", healthHandler.Live)
	r.GET("/readyz", healthHandler.Ready)
	r.GET("/metrics", adminrest.MetricsHandler())

	api := r.Group("/api/v1")

	var serverAuthHandler *adminrest.AuthHandler
	if cfg.Auth.Enabled {
		sessionKey, err := crypto.DeriveSessionHMACKey(cfg.Auth.APIKey)
		if err != nil {
			panic("http.NewServer: derive session HMAC key: " + err.Error())
		}
		// M1: stricter limiter for /auth/password — 3 attempts / 15min,
		// per ClientIP. Independent from the login limiter so a brute-
		// forcer with a stolen cookie can't exhaust BOTH paths.
		passwordLimiter := auth.NewIPLimiter(auth.PasswordChangeLimit(), 3)
		// Story 485 (N-7a): if a shared AuthRuntime pointer was supplied,
		// seed its boot defaults (SessionTTL + SecureCookie + Mode=forms)
		// here so the AuthHandler and MeHandler observe the same initial
		// values BEFORE the reload subscriber publishes the first snapshot.
		// Without this seed, the shared atomic would carry SessionTTL=0 and
		// Login would issue a cookie with max-age=0 on the very first
		// request after boot.
		if sharedAuthRuntime != nil {
			sharedAuthRuntime.Store(&middleware.AuthRuntime{
				Mode:         runtime.AuthModeForms,
				SessionTTL:   cfg.Auth.SessionTTL,
				SecureCookie: cfg.Auth.SecureCookie,
			})
		}
		authHandler := adminrest.NewAuthHandler(
			cfg.Auth.APIKey, adminRepo, cfg.Auth.SessionTTL,
			cfg.Auth.SecureCookie, loginLimiter, logger,
			adminrest.WithPasswordLimiter(passwordLimiter),
			adminrest.WithAuthRuntimePointer(sharedAuthRuntime),
		)
		// Hold a reference so the reload subscriber can pull the
		// shared AuthRuntime pointer out at startup.
		serverAuthHandler = authHandler
		api.POST("/auth/login", authHandler.Login)
		// Public bootstrap endpoint — MUST be registered before the
		// guarded group so it inherits NO RequireAuth middleware.
		// Reads from the same AuthRuntime atomic the dispatcher uses.
		authConfigHandler := adminrest.NewAuthConfigHandler(authHandler.AuthRuntime())
		api.GET("/auth/config", authConfigHandler.Get)

		oidcHandler := adminrest.NewOIDCHandler(
			oidcUC, authHandler.AuthRuntime(), sessionKey,
			cfg.Auth.SessionTTL, cfg.Auth.SecureCookie, logger,
		)
		api.GET("/auth/oidc/start", oidcHandler.Start)
		api.GET("/auth/oidc/callback", oidcHandler.Callback)

		guarded := api.Group("")
		guarded.Use(middleware.RequireAuthWithRuntime(
			cfg.Auth.APIKey, sessionKey, authHandler.AuthRuntime(),
			adminRepo, loginLimiter,
		))
		guarded.GET("/auth/session", authHandler.Session)
		guarded.DELETE("/auth/session", authHandler.Logout)
		guarded.POST("/auth/password", authHandler.PasswordChange)
		guarded.POST("/scan", scanHandler.Trigger)
		// Story 492 / N-1b — per-instance series-scoped routes DELETED.
		// `/missing` (no replacement; FE drops the consumer), per-
		// instance counters, the series-cache list / networks facet,
		// the series-detail document, refresh trigger, season detail,
		// cast, torrents, and the season-episodes upstream fetch all
		// move to the global namespace (`/series/...`) or are dropped
		// entirely. The per-instance handler STRUCTS stay alive —
		// they're reached via the global wrappers' c.Params splice —
		// only the route registrations drop. The `/instances` list
		// endpoint also moves under `/admin/instances` per PRD §4828.
		// The aggregate `/counters` (global cross-instance) stays.
		countersHandler := catalogrest.NewCountersHandler(instanceReg, counterRepo, logger)
		// Story 217 (H-2) — person detail page. Top-level resource —
		// `/people` is instance-independent. Nil-OK pattern matches
		// seriesCastHandler.
		if peopleHandler != nil {
			guarded.GET("/people/:tmdbId", peopleHandler.Get)
		}
		// Story 491 / N-1a — global series surface. Routes resolved by
		// canonical series.id rather than per-instance Sonarr id.
		// Register `/series/networks` BEFORE `/series/:id` for clarity
		// (gin radix tree handles static-before-param anyway, but
		// declaration order matches reader expectations).
		guarded.GET("/series/networks", globalCatalogHandler.Networks)
		guarded.GET("/series", globalCatalogHandler.List)
		// Story 578 / E-1-B5 — weak-ETag / Cache-Control on the
		// enrichment-backed canon-detail GETs. Built once, shared across
		// routes (stateless). gin runs it before each handler; on a
		// 304 / fail-open path it either aborts or c.Next()s untouched.
		// Deliberately NOT wired onto POST /regrab (mutating), /torrents
		// or /library (per-instance *Arr state, no enrichment stamp).
		etagMW := ETagMiddleware(etagFreshness, logger)
		if globalSeriesHandler != nil {
			guarded.GET("/series/:id", etagMW, globalSeriesHandler.Get)
			guarded.POST("/series/:id/regrab", globalSeriesHandler.Regrab)
		}
		// Story 492 / N-1b — global series-scoped surfaces.
		if globalCastHandler != nil {
			guarded.GET("/series/:id/cast", etagMW, globalCastHandler.Get)
		}
		// Story 529 — decomposition 1/3: /series/:id/overview split.
		if globalOverviewHandler != nil {
			guarded.GET("/series/:id/overview", etagMW, globalOverviewHandler.Get)
		}
		// Story 530 — decomposition 2/3: /series/:id/recommendations split.
		if globalRecommendationsHandler != nil {
			guarded.GET("/series/:id/recommendations", etagMW, globalRecommendationsHandler.Get)
		}
		if globalSeasonHandler != nil {
			guarded.GET("/series/:id/season/:n", etagMW, globalSeasonHandler.Get)
		}
		guarded.GET("/series/:id/seasons/:season/episodes", etagMW, globalSeasonEpisodesHandler.Get)
		if globalTorrentsHandler != nil {
			guarded.GET("/series/:id/torrents", globalTorrentsHandler.Get)
		}
		// Story 577 / E-1-B2 — per-instance Sonarr library-state endpoint.
		if globalLibraryHandler != nil {
			guarded.GET("/series/:id/library", globalLibraryHandler.Get)
		}
		// Story 582 / E-1 B3c — canon list-of-seasons (posters + counts).
		if seasonsHandler != nil {
			guarded.GET("/series/:id/seasons", seasonsHandler.Get)
		}
		guarded.GET("/grabs/:id/episode-files", globalGrabEpisodeFilesHandler.List)
		// F-1 (Story 214): content-addressed media proxy. Serves the
		// canonical TMDB image variants pre-warmed by the series
		// enrichment worker. mediaHandler is nil-OK — when wiring is
		// disabled (tests / minimal boot) the route is omitted.
		//
		// HEAD is registered alongside GET so probes (curl -I, browser
		// prefetch, CDN warmup, monitoring) don't fall through to the
		// default Gin 404. The handler's c.Data writes the same headers
		// for HEAD — Gin's writer suppresses the body automatically.
		//
		// W16-2: registered on the PUBLIC `api` group (NOT `guarded`) so
		// unauthenticated <img> tags, incognito sessions, and CDN warmers
		// get the bytes (200/304) instead of a 401. The URLs are opaque
		// sha256 content-addressed image bytes (no PII, no enumeration),
		// and the handler reads no auth/session context — safe to expose.
		if mediaHandler != nil {
			api.GET("/media/:hash", mediaHandler.Serve)
			api.HEAD("/media/:hash", mediaHandler.Serve)
		}
		qbitDiscoverHandler := handlers.NewQbitDiscoverHandler(instanceReg, logger)
		guarded.GET("/instances/:name/discover/qbit", qbitDiscoverHandler.Discover)
		webhookInstallHandler := catalogrest.NewWebhookInstallHandler(webhookReconciler, webhookStatusCache, logger)
		guarded.POST("/instances/:name/webhook/install", reconcileContextMiddleware(), webhookInstallHandler.Install)
		webhookStatusHandler := catalogrest.NewWebhookStatusHandler(webhookReconciler, logger)
		guarded.GET("/instances/:name/webhook/status", reconcileContextMiddleware(), webhookStatusHandler.Status)
		// Story 492 / N-1b — admin instance management moves under
		// `/admin/instances/...`. Atomic flip (no co-registration) — the
		// FE story 493 swaps every call site in the same PR.
		guarded.GET("/admin/instances", instancesHandler.List)
		guarded.GET("/admin/instances/:name", instanceCRUD.Get)
		guarded.POST("/admin/instances", reconcileContextMiddleware(), instanceCRUD.Create)
		guarded.PUT("/admin/instances/:name", reconcileContextMiddleware(), instanceCRUD.Update)
		guarded.DELETE("/admin/instances/:name", reconcileContextMiddleware(), instanceCRUD.Delete)
		guarded.POST("/admin/instances/test",
			probeRateLimit(loginLimiter),
			instanceProbe.Test,
		)
		// Story 507 (N-2f) — curated discovery read endpoints.
		// Nil-OK pattern: when wiring did not construct the handler
		// (TMDB disabled at boot or test wiring) the routes are
		// omitted rather than 5xx-stubbed.
		if discoveryHandler != nil {
			guarded.GET("/discovery/trending", discoveryHandler.Trending)
			guarded.GET("/discovery/popular", discoveryHandler.Popular)
			guarded.GET("/discovery/genre/:id", discoveryHandler.ByGenre)
			guarded.GET("/discovery/network/:id", discoveryHandler.ByNetwork)
			guarded.GET("/discovery/keyword/:id", discoveryHandler.ByKeyword)
			guarded.GET("/discovery/genres", discoveryHandler.PickerGenres)
			guarded.GET("/discovery/networks", discoveryHandler.PickerNetworks)
			// Story 508 (N-2g) — local LIKE + TMDB fallback search.
			guarded.GET("/discovery/search", discoveryHandler.Search)
		}
		if discoverHandler != nil {
			// Story 509 (N-2h) — ad-hoc TMDB Discover passthrough with LRU
			// + background fetcher Pattern B (PRD §5.1.2).
			guarded.GET("/discovery/discover", discoverHandler.Handle)
		}
		// Story 520 (N-4c) — POST add-to-sonarr. Nil-OK pattern: when
		// wiring did not construct the handler (test bootstrap) the
		// route is omitted rather than 5xx-stubbed.
		if addToSonarrHandler != nil {
			guarded.POST("/discovery/add-to-sonarr", addToSonarrHandler.Handle)
		}
		if qbitSettings != nil {
			guarded.GET("/instances/:name/qbit/settings", qbitSettings.Get)
			guarded.PUT("/instances/:name/qbit/settings", qbitSettings.Upsert)
			guarded.DELETE("/instances/:name/qbit/settings", qbitSettings.Delete)
		}
		// Story 519 (N-4b) — per-instance metadata cache surface for the
		// AddToSonarrModal pickers (quality profiles + root folders) +
		// operator-driven cache invalidation. Nil-OK pattern mirrors
		// qbitSettings so test wiring can omit the routes.
		if instanceMetadataHandler != nil {
			guarded.GET("/instances/:name/quality-profiles", instanceMetadataHandler.GetQualityProfiles)
			guarded.GET("/instances/:name/root-folders", instanceMetadataHandler.GetRootFolders)
			guarded.POST("/instances/:name/refresh-metadata", instanceMetadataHandler.RefreshMetadata)
			// Story 524 N-4 per-season picker — uncached lookup proxy.
			guarded.GET("/instances/:name/sonarr-lookup", instanceMetadataHandler.SonarrLookup)
		}
		if externalServices != nil {
			guarded.GET("/external-services", externalServices.List)
			guarded.PUT("/external-services/:service", externalServices.Upsert)
			guarded.POST("/external-services/:service/test", externalServices.Test)
		}
		guarded.GET("/instances/:name/watchdog/rollups", watchdogRollupHandler.One)
		guarded.GET("/watchdog/rollups", watchdogRollupHandler.All)
		guarded.GET("/instances/:name/watchdog/blacklist", watchdogBlacklistHandler.List)
		guarded.DELETE("/instances/:name/watchdog/blacklist/:series/:season", watchdogBlacklistHandler.Delete)
		if watchdogSeasonsHandler != nil {
			guarded.GET("/watchdog/seasons", watchdogSeasonsHandler.List)
			guarded.GET("/watchdog/series/:instance/:id", watchdogSeasonsHandler.Series)
		}
		guarded.GET("/webhooks/status", webhooksAggregateHandler.Status)
		guarded.GET("/scans", auditHandler.ListScans)
		guarded.GET("/scans/:id", auditHandler.GetScan)
		guarded.GET("/decisions", auditHandler.ListDecisions)
		guarded.GET("/decisions/:id", auditHandler.GetDecision)
		guarded.GET("/grabs", auditHandler.ListGrabs)
		guarded.GET("/counters", countersHandler.Aggregate)
		// Story 492 / N-1b — per-instance grab episode-files moved to
		// the global namespace (`/grabs/:id/episode-files`); see route
		// registration in the N-1b block above. The per-instance
		// handler struct stays in `internal/grab/rest/grab_episode_files.go`
		// for its own test coverage but is no longer reached via any
		// HTTP route.
		guarded.POST("/decisions/:id/grab", grabHandler.ByDecision)
		rescanHandler := watchdogrest.NewRescanHandler(rescanUC, logger)
		guarded.POST("/decisions/:id/rescan", rescanHandler.ByDecision)
		guarded.POST("/scans/:id/cancel", scanHandler.Cancel)
		guarded.GET("/config/runtime", runtimeConfigHandler.Get)
		guarded.PUT("/config/runtime", runtimeConfigHandler.Update)
		if timezoneHandler != nil {
			guarded.GET("/settings/timezone", timezoneHandler.Get)
			guarded.PATCH("/settings/timezone", timezoneHandler.Patch)
		}

		// Story 485 (N-7a) — current-user profile + settings patch +
		// change-password. Nil-OK pattern mirrors timezoneHandler: when
		// the wirer skipped construction (test / minimal boot) the routes
		// are omitted rather than 5xx-stubbed.
		//
		// /me/change-password reuses the SAME per-IP passwordLimiter the
		// legacy /auth/password sits behind via a tiny adapter (gin doesn't
		// expose the parent group's middleware as a slice).
		if meHandler != nil {
			guarded.GET("/me", meHandler.Get)
			guarded.PATCH("/me/settings", meHandler.UpdateSettings)
			guarded.POST("/me/change-password",
				passwordLimiterMiddleware(passwordLimiter),
				meHandler.ChangePassword,
			)
		}

		oidcTestHandler := adminrest.NewOIDCTestHandler(authHandler.AuthRuntime(), logger)
		guarded.POST("/auth/oidc/test", oidcTestHandler.Test)

		// Webhook on the shared auth surface + per-instance rate limit.
		wh := api.Group("/webhook/sonarr/:instance_name")
		// Webhook is mode-invariant AND local-bypass-invariant per
		// D-3 / AC-8 — X-Api-Key only. RequireAuthWebhook pins the
		// local-bypass branch off so a local IP can NEVER POST a
		// webhook without a valid X-Api-Key. Mode dispatch still
		// runs but in practice Sonarr always sends the key.
		wh.Use(middleware.RequireAuthWebhook(
			cfg.Auth.APIKey, sessionKey, authHandler.AuthRuntime(),
			adminRepo, loginLimiter,
		))
		if webhookLimiter != nil {
			wh.Use(webhookRateLimit(webhookLimiter))
		}
		wh.POST("", webhookHandler.Handle)
	} else {
		// HIGH-S1: if an operator flips auth.enabled=false they get an
		// unusable service (only /healthz, /readyz, /metrics). Make the
		// state loud at startup so it's not mistaken for "API broken".
		// auth.enabled=false is documented as a testing-only mode; the
		// production config keeps it true.
		logger.Warn("auth disabled — only /healthz, /readyz, /metrics exposed; API routes NOT registered",
			slog.String("hint", "set http.auth.enabled=true to expose /api/v1/*"))
	}

	srv := &http.Server{
		Addr:         cfg.Bind,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	return &Server{cfg: cfg, server: srv, engine: r, authHandler: serverAuthHandler, logger: logger}
}

// passwordLimiterMiddleware adapts an *auth.IPLimiter into a Gin
// middleware that mirrors the inline 429 envelope used by the legacy
// PasswordChange handler. Pulled out so /me/change-password reuses the
// SAME limiter instance constructed in NewServer rather than allocating
// a parallel one.
//
// The limiter is nil-OK so test wiring that omits cfg.Auth.Enabled gets
// a pass-through middleware. Story 485 (N-7a).
func passwordLimiterMiddleware(lim *auth.IPLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if lim != nil && !lim.Allow(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded", "code": "RATE_LIMITED",
			})
			return
		}
		c.Next()
	}
}

// probeRateLimit reuses the login limiter so a brute-forcer can't
// turn POST /instances/test into a side-channel oracle on internal
// URLs. Keyed on ClientIP (same as Login).
func probeRateLimit(lim *auth.IPLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if lim == nil {
			c.Next()
			return
		}
		if !lim.Allow(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded", "code": "RATE_LIMITED",
			})
			return
		}
		c.Next()
	}
}

// webhookRateLimit keys on :instance_name. IP-keyed would be wrong
// here — Sonarr always comes from the same IP, but per-instance keeps
// one rogue instance from starving the others.
func webhookRateLimit(lim *auth.IPLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !lim.Allow(c.Param("instance_name")) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

func (s *Server) Start() error {
	s.logger.Info("starting http server", slog.String("addr", s.cfg.Bind))
	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	timeout := s.cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.server.Shutdown(sctx)
}

// Engine returns the underlying gin engine. The reload
// authMiddleware subscriber calls SetTrustedProxies on the engine
// when `trusted_proxies` changes; no other caller should reach for
// this — every legitimate handler is registered at construction.
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

// AuthHandler returns the handler if auth is enabled, or nil
// otherwise. Used by the reload subscriber to obtain the shared
// AuthRuntime pointer for in-process TTL swaps.
func (s *Server) AuthHandler() *adminrest.AuthHandler {
	return s.authHandler
}

// reconcileContextMiddleware extracts the seasonfill public URL from
// X-Forwarded-Proto + X-Forwarded-Host (falling back to Request.Host
// + TLS state) and stashes it on the request context under the key
// webhookinstall.PublicURLFromContext reads. This is the bridge that
// lets the reconciler — which lives in the application layer — get
// the per-request public URL without depending on gin.Context.
//
// The CRUD path (POST/PUT /instances) needs the same hook so the sync
// reconcile inside instance.UseCase has a public URL to derive from.
// Apply via guarded.Use(...) instead of per-route for the CRUD group
// — see the wiring change below.
func reconcileContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
		if host == "" {
			host = strings.TrimSpace(c.Request.Host)
		}
		scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
		if scheme == "" {
			if c.Request.TLS != nil {
				scheme = "https"
			} else {
				scheme = "http"
			}
		}
		if host != "" {
			ctx := context.WithValue(c.Request.Context(),
				webhookinstall.RequestPublicURLKey{},
				scheme+"://"+host)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
}
