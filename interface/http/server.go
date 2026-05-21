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
	logger *slog.Logger
}

func NewServer(
	cfg config.HTTPConfig,
	scanUC *scan.UseCase,
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
	scanHandler := handlers.NewScanHandler(scanUC)
	instancesHandler := handlers.NewInstancesHandler(checker)
	auditHandler := handlers.NewAuditHandler(scanRepo, decisionRepo, grabRepo)

	r.GET("/healthz", healthHandler.Live)
	r.GET("/readyz", healthHandler.Ready)
	r.GET("/metrics", handlers.MetricsHandler())

	api := r.Group("/api/v1")
	if cfg.Auth.Enabled {
		api.Use(middleware.APIKeyAuth(cfg.Auth.APIKey))
	}
	api.POST("/scan", scanHandler.Trigger)
	api.GET("/instances", instancesHandler.List)
	api.GET("/scans", auditHandler.ListScans)
	api.GET("/scans/:id", auditHandler.GetScan)
	api.GET("/decisions", auditHandler.ListDecisions)
	api.GET("/grabs", auditHandler.ListGrabs)

	srv := &http.Server{
		Addr:         cfg.Bind,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	return &Server{cfg: cfg, server: srv, logger: logger}
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
