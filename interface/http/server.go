package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/auth"
	appgrab "github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/ports"
	apprescan "github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

type Server struct {
	cfg         config.HTTPConfig
	server      *http.Server
	engine      *gin.Engine
	authHandler *handlers.AuthHandler
	logger      *slog.Logger
}

func NewServer(
	cfg config.HTTPConfig,
	scanUC *scan.UseCase,
	webhookUC handlers.WebhookProcessor,
	checker *healthcheck.Checker,
	scanRepo ports.ScanRepository,
	decisionRepo ports.DecisionRepository,
	grabRepo ports.GrabRepository,
	adminRepo ports.AdminUserRepository,
	loginLimiter *auth.IPLimiter,
	webhookLimiter *auth.IPLimiter,
	instanceReg handlers.InstanceRegistry,
	cooldownRepo ports.CooldownRepository,
	grabUC *appgrab.UseCase,
	rescanUC *apprescan.UseCase,
	instanceCRUD *handlers.InstanceCRUDHandler,
	instanceProbe *handlers.InstanceProbeHandler,
	runtimeConfigHandler *handlers.RuntimeConfigHandler,
	qbitSettings *handlers.QbitSettingsHandler,
	oidcUC *auth.OIDCLoginUseCase,
	webhookReconciler *webhookinstall.Reconciler,
	webhookStatusCache *webhookinstall.StatusCache,
	seriesCacheRepo ports.SeriesCacheRepository,
	counterRepo ports.CounterRepository,
	watchdogRollupHandler *handlers.WatchdogRollupHandler,
	watchdogBlacklistHandler *handlers.WatchdogBlacklistHandler,
	webhooksAggregateHandler *handlers.WebhooksAggregateHandler,
	logger *slog.Logger,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLogging(logger))

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

	healthHandler := handlers.NewHealthHandler(checker)
	scanHandler := handlers.NewScanHandler(scanUC, logger)
	instancesHandler := handlers.NewInstancesHandler(checker, instanceReg, logger).
		WithSeriesCache(seriesCacheRepo)
	auditHandler := handlers.NewAuditHandler(scanRepo, decisionRepo, grabRepo, logger)
	webhookHandler := handlers.NewWebhookHandler(webhookUC, instanceReg, logger)
	grabHandler := handlers.NewGrabHandler(decisionRepo, grabRepo, cooldownRepo, grabUC, instanceReg, logger)

	r.GET("/healthz", healthHandler.Live)
	r.GET("/readyz", healthHandler.Ready)
	r.GET("/metrics", handlers.MetricsHandler())

	api := r.Group("/api/v1")

	var serverAuthHandler *handlers.AuthHandler
	if cfg.Auth.Enabled {
		sessionKey, err := crypto.DeriveSessionHMACKey(cfg.Auth.APIKey)
		if err != nil {
			panic("http.NewServer: derive session HMAC key: " + err.Error())
		}
		// M1: stricter limiter for /auth/password — 3 attempts / 15min,
		// per ClientIP. Independent from the login limiter so a brute-
		// forcer with a stolen cookie can't exhaust BOTH paths.
		passwordLimiter := auth.NewIPLimiter(auth.PasswordChangeLimit(), 3)
		authHandler := handlers.NewAuthHandler(
			cfg.Auth.APIKey, adminRepo, cfg.Auth.SessionTTL,
			cfg.Auth.SecureCookie, loginLimiter, logger,
			handlers.WithPasswordLimiter(passwordLimiter),
		)
		// Hold a reference so the reload subscriber can pull the
		// shared AuthRuntime pointer out at startup.
		serverAuthHandler = authHandler
		api.POST("/auth/login", authHandler.Login)
		// Public bootstrap endpoint — MUST be registered before the
		// guarded group so it inherits NO RequireAuth middleware.
		// Reads from the same AuthRuntime atomic the dispatcher uses.
		authConfigHandler := handlers.NewAuthConfigHandler(authHandler.AuthRuntime())
		api.GET("/auth/config", authConfigHandler.Get)

		oidcHandler := handlers.NewOIDCHandler(
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
		guarded.GET("/instances", instancesHandler.List)
		guarded.GET("/instances/:name/missing", instancesHandler.Missing)
		guarded.GET("/instances/:name/series/:id/seasons/:season/episodes", instancesHandler.SeasonEpisodes)
		countersHandler := handlers.NewCountersHandler(instanceReg, counterRepo, logger)
		guarded.GET("/instances/:name/counters", countersHandler.ForInstance)
		guarded.GET("/instances/:name/series-cache", instancesHandler.ListSeriesCache)
		guarded.GET("/instances/:name/series", instancesHandler.SearchSeries)
		// Singleton poster cache. Lives for the life of the process
		// — there's no reload path because the cap + TTL are
		// package-level constants (see internal/runtime/snapshot.go).
		posterCache := sonarr.NewLRUPosterCache(
			runtime.PosterCacheMaxBytes, runtime.PosterCacheTTL)
		seriesPosterHandler := handlers.NewSeriesPosterHandler(
			instanceReg, logger, handlers.WithPosterCache(posterCache))
		guarded.GET("/instances/:name/series/:id/poster", seriesPosterHandler.Proxy)
		qbitDiscoverHandler := handlers.NewQbitDiscoverHandler(instanceReg, logger)
		guarded.GET("/instances/:name/discover/qbit", qbitDiscoverHandler.Discover)
		webhookInstallHandler := handlers.NewWebhookInstallHandler(webhookReconciler, webhookStatusCache, logger)
		guarded.POST("/instances/:name/webhook/install", reconcileContextMiddleware(), webhookInstallHandler.Install)
		webhookStatusHandler := handlers.NewWebhookStatusHandler(webhookReconciler, logger)
		guarded.GET("/instances/:name/webhook/status", reconcileContextMiddleware(), webhookStatusHandler.Status)
		guarded.GET("/instances/:name", instanceCRUD.Get)
		guarded.POST("/instances", reconcileContextMiddleware(), instanceCRUD.Create)
		guarded.PUT("/instances/:name", reconcileContextMiddleware(), instanceCRUD.Update)
		guarded.DELETE("/instances/:name", reconcileContextMiddleware(), instanceCRUD.Delete)
		guarded.POST("/instances/test",
			probeRateLimit(loginLimiter),
			instanceProbe.Test,
		)
		if qbitSettings != nil {
			guarded.GET("/instances/:name/qbit/settings", qbitSettings.Get)
			guarded.PUT("/instances/:name/qbit/settings", qbitSettings.Upsert)
			guarded.DELETE("/instances/:name/qbit/settings", qbitSettings.Delete)
		}
		guarded.GET("/instances/:name/watchdog/rollups", watchdogRollupHandler.One)
		guarded.GET("/watchdog/rollups", watchdogRollupHandler.All)
		guarded.GET("/instances/:name/watchdog/blacklist", watchdogBlacklistHandler.List)
		guarded.DELETE("/instances/:name/watchdog/blacklist/:id", watchdogBlacklistHandler.Delete)
		guarded.GET("/webhooks/status", webhooksAggregateHandler.Status)
		guarded.GET("/scans", auditHandler.ListScans)
		guarded.GET("/scans/:id", auditHandler.GetScan)
		guarded.GET("/decisions", auditHandler.ListDecisions)
		guarded.GET("/decisions/:id", auditHandler.GetDecision)
		guarded.GET("/grabs", auditHandler.ListGrabs)
		guarded.GET("/counters", countersHandler.Aggregate)
		grabEpisodeFilesHandler := handlers.NewGrabEpisodeFilesHandler(grabRepo, instanceReg, logger)
		guarded.GET("/instances/:name/grabs/:id/episode-files", grabEpisodeFilesHandler.List)
		guarded.POST("/decisions/:id/grab", grabHandler.ByDecision)
		rescanHandler := handlers.NewRescanHandler(rescanUC, logger)
		guarded.POST("/decisions/:id/rescan", rescanHandler.ByDecision)
		guarded.POST("/scans/:id/cancel", scanHandler.Cancel)
		guarded.GET("/config/runtime", runtimeConfigHandler.Get)
		guarded.PUT("/config/runtime", runtimeConfigHandler.Update)

		oidcTestHandler := handlers.NewOIDCTestHandler(authHandler.AuthRuntime(), logger)
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
func (s *Server) AuthHandler() *handlers.AuthHandler {
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
