package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
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
	webhookCfg config.WebhookConfig,
	scanUC *scan.UseCase,
	webhookUC handlers.WebhookProcessor,
	checker *healthcheck.Checker,
	scanRepo ports.ScanRepository,
	decisionRepo ports.DecisionRepository,
	grabRepo ports.GrabRepository,
	logger *slog.Logger,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLogging(logger))

	healthHandler := handlers.NewHealthHandler(checker)
	scanHandler := handlers.NewScanHandler(scanUC, logger)
	instancesHandler := handlers.NewInstancesHandler(checker)
	auditHandler := handlers.NewAuditHandler(scanRepo, decisionRepo, grabRepo, logger)
	webhookHandler := handlers.NewWebhookHandler(webhookUC, webhookCfg, logger)

	r.GET("/healthz", healthHandler.Live)
	r.GET("/readyz", healthHandler.Ready)
	r.GET("/metrics", handlers.MetricsHandler())

	api := r.Group("/api/v1")

	// Admin + auth routes are mounted ONLY when auth is enabled.
	// 009a's NewAuthHandler panics on empty APIKey (H2 review-fix:
	// fail-fast on server misconfig), so we MUST NOT construct it
	// when Enabled=false (which is also when APIKey may be empty,
	// e.g. test fixtures via buildServer(adminKey="")). The
	// pre-existing no-auth path keeps producing a server with zero
	// /api/v1 routes mounted — callers that needed those routes
	// always supplied an APIKey and toggled Enabled=true.
	if cfg.Auth.Enabled {
		// secureCookie is its own knob — auth.enabled toggles the
		// admin surface; secure_cookie says "are we behind HTTPS?".
		// Conflating them breaks http://localhost dev (browser drops
		// Secure cookie on HTTP) — see M1 review-fix.
		authHandler := handlers.NewAuthHandler(cfg.Auth.APIKey, cfg.Auth.CookieSecret, cfg.Auth.SecureCookie, logger)

		// Auth endpoints. Login MUST NOT require auth (otherwise no one
		// can log in); Logout DOES (only authenticated browsers clear
		// their session).
		auth := api.Group("/auth")
		auth.POST("/login", authHandler.Login)
		authGuarded := auth.Group("")
		authGuarded.Use(middleware.RequireAuth(cfg.Auth.APIKey, cfg.Auth.CookieSecret))
		authGuarded.GET("/session", authHandler.Session)
		authGuarded.DELETE("/session", authHandler.Logout)

		// Existing admin routes: swap APIKeyAuth → RequireAuth.
		// Strict superset (cookie OR header now accepted).
		apiGuarded := api.Group("")
		apiGuarded.Use(middleware.RequireAuth(cfg.Auth.APIKey, cfg.Auth.CookieSecret))
		apiGuarded.POST("/scan", scanHandler.Trigger)
		apiGuarded.GET("/instances", instancesHandler.List)
		apiGuarded.GET("/scans", auditHandler.ListScans)
		apiGuarded.GET("/scans/:id", auditHandler.GetScan)
		apiGuarded.GET("/decisions", auditHandler.ListDecisions)
		apiGuarded.GET("/grabs", auditHandler.ListGrabs)
	}

	// Webhook is independent — mounted on the root engine so admin
	// RequireAuth never inherits. Applies its own APIKeyAuth (or
	// none when Webhook.Secret is empty).
	wh := r.Group("/api/v1/webhook/sonarr/:instance_name")
	if webhookCfg.Secret != "" {
		wh.Use(middleware.APIKeyAuth(webhookCfg.Secret))
	} else {
		logger.Warn("webhook_auth_disabled",
			slog.String("reason",
				"webhook.secret empty — relying on NetworkPolicy / upstream firewall"),
		)
	}
	wh.POST("", webhookHandler.Handle)

	srv := &http.Server{
		Addr:         cfg.Bind,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	return &Server{cfg: cfg, server: srv, engine: r, logger: logger}
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
