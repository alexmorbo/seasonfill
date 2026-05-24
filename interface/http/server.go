package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/auth"
	appgrab "github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/ports"
	apprescan "github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/config"
)

type Server struct {
	cfg    config.HTTPConfig
	server *http.Server
	engine *gin.Engine
	logger *slog.Logger
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
	sonarrClients map[string]ports.SonarrClient,
	instanceModes map[string]string,
	knownInstances map[string]struct{},
	cooldownRepo ports.CooldownRepository,
	grabUC *appgrab.UseCase,
	rescanUC *apprescan.UseCase,
	instancesByName map[string]scan.Instance,
	instanceCRUD *handlers.InstanceCRUDHandler,
	instanceProbe *handlers.InstanceProbeHandler,
	runtimeConfigHandler *handlers.RuntimeConfigHandler,
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
	instancesHandler := handlers.NewInstancesHandler(checker, sonarrClients, instanceModes, logger)
	auditHandler := handlers.NewAuditHandler(scanRepo, decisionRepo, grabRepo, logger)
	webhookHandler := handlers.NewWebhookHandler(webhookUC, knownInstances, logger)
	grabHandler := handlers.NewGrabHandler(decisionRepo, grabRepo, cooldownRepo, grabUC, instancesByName, logger)

	r.GET("/healthz", healthHandler.Live)
	r.GET("/readyz", healthHandler.Ready)
	r.GET("/metrics", handlers.MetricsHandler())

	api := r.Group("/api/v1")

	if cfg.Auth.Enabled {
		// M1: stricter limiter for /auth/password — 3 attempts / 15min,
		// per ClientIP. Independent from the login limiter so a brute-
		// forcer with a stolen cookie can't exhaust BOTH paths.
		passwordLimiter := auth.NewIPLimiter(auth.PasswordChangeLimit(), 3)
		authHandler := handlers.NewAuthHandler(
			cfg.Auth.APIKey, adminRepo, cfg.Auth.SessionTTL,
			cfg.Auth.SecureCookie, loginLimiter, logger,
			handlers.WithPasswordLimiter(passwordLimiter),
		)
		api.POST("/auth/login", authHandler.Login)

		guarded := api.Group("")
		guarded.Use(middleware.RequireAuth(cfg.Auth.APIKey))
		guarded.GET("/auth/session", authHandler.Session)
		guarded.DELETE("/auth/session", authHandler.Logout)
		guarded.POST("/auth/password", authHandler.PasswordChange)
		guarded.POST("/scan", scanHandler.Trigger)
		guarded.GET("/instances", instancesHandler.List)
		guarded.GET("/instances/:name/missing", instancesHandler.Missing)
		guarded.GET("/instances/:name/series", instancesHandler.SearchSeries)
		guarded.GET("/instances/:name", instanceCRUD.Get)
		guarded.POST("/instances", instanceCRUD.Create)
		guarded.PUT("/instances/:name", instanceCRUD.Update)
		guarded.DELETE("/instances/:name", instanceCRUD.Delete)
		guarded.POST("/instances/test",
			probeRateLimit(loginLimiter),
			instanceProbe.Test,
		)
		guarded.GET("/scans", auditHandler.ListScans)
		guarded.GET("/scans/:id", auditHandler.GetScan)
		guarded.GET("/decisions", auditHandler.ListDecisions)
		guarded.GET("/grabs", auditHandler.ListGrabs)
		guarded.POST("/decisions/:id/grab", grabHandler.ByDecision)
		rescanHandler := handlers.NewRescanHandler(rescanUC, logger)
		guarded.POST("/decisions/:id/rescan", rescanHandler.ByDecision)
		guarded.POST("/scans/:id/cancel", scanHandler.Cancel)
		guarded.GET("/config/runtime", runtimeConfigHandler.Get)
		guarded.PUT("/config/runtime", runtimeConfigHandler.Update)

		// Webhook on the shared auth surface + per-instance rate limit.
		wh := api.Group("/webhook/sonarr/:instance_name")
		wh.Use(middleware.RequireAuth(cfg.Auth.APIKey))
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
	return &Server{cfg: cfg, server: srv, engine: r, logger: logger}
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
