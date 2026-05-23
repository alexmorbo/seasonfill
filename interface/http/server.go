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
	logger *slog.Logger,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLogging(logger))

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
		authHandler := handlers.NewAuthHandler(
			cfg.Auth.APIKey, adminRepo, cfg.Auth.SessionTTL,
			cfg.Auth.SecureCookie, loginLimiter, logger,
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
		guarded.GET("/scans", auditHandler.ListScans)
		guarded.GET("/scans/:id", auditHandler.GetScan)
		guarded.GET("/decisions", auditHandler.ListDecisions)
		guarded.GET("/grabs", auditHandler.ListGrabs)
		guarded.POST("/decisions/:id/grab", grabHandler.ByDecision)
		rescanHandler := handlers.NewRescanHandler(rescanUC, logger)
		guarded.POST("/decisions/:id/rescan", rescanHandler.ByDecision)
		guarded.POST("/scans/:id/cancel", scanHandler.Cancel)

		// Webhook on the shared auth surface + per-instance rate limit.
		wh := api.Group("/webhook/sonarr/:instance_name")
		wh.Use(middleware.RequireAuth(cfg.Auth.APIKey))
		if webhookLimiter != nil {
			wh.Use(webhookRateLimit(webhookLimiter))
		}
		wh.POST("", webhookHandler.Handle)
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
