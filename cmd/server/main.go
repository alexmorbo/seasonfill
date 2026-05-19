package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	httpserver "github.com/alexmorbo/seasonfill/interface/http"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "Path to YAML configuration")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logger.New(logger.Config{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
		Output: os.Stdout,
	})
	slog.SetDefault(log)

	log.Info("starting seasonfill",
		slog.String("driver", cfg.Database.Driver),
		slog.Int("instances", len(cfg.SonarrInstances)),
		slog.Bool("dry_run", cfg.DryRun),
	)

	db, err := database.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	scanRepo := repositories.NewScanRepository(db)
	decisionRepo := repositories.NewDecisionRepository(db)

	scanInstances := make([]scan.Instance, 0, len(cfg.SonarrInstances))
	sonarrClients := make([]ports.SonarrClient, 0, len(cfg.SonarrInstances))
	for _, sc := range cfg.SonarrInstances {
		rps, burst := sc.RateLimit.RPS, sc.RateLimit.Burst
		if rps == 0 {
			rps = 5
		}
		if burst == 0 {
			burst = 10
		}
		limiter := ratelimit.New(rps, burst)
		c := sonarr.NewWithLimiter(sc.Name, sc.URL, sc.APIKey, sc.Timeout, limiter, log)
		sonarrClients = append(sonarrClients, c)
		scanInstances = append(scanInstances, scan.Instance{Config: sc, Client: c})
	}

	checker := healthcheck.New(db, sonarrClients)

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	go checker.Run(rootCtx, 30*time.Second)

	evaluator := evaluate.NewPerInstanceUseCase(decisionRepo, log)
	scanUC := scan.NewUseCase(scanInstances, evaluator, scanRepo, log, cfg.DryRun)

	httpServer := httpserver.NewServer(cfg.HTTP, scanUC, checker, log)

	var sched *scheduler.Scheduler
	if cfg.Cron.Enabled {
		sched = scheduler.New(cfg.Cron.Schedule, cfg.Cron.Jitter, log)
		if err := sched.Start(rootCtx, scanUC); err != nil {
			return fmt.Errorf("start scheduler: %w", err)
		}
		if cfg.Cron.OnStart {
			go func() {
				if _, err := scanUC.Run(rootCtx, scan.TriggerStartup); err != nil && !errors.Is(err, scan.ErrScanAlreadyRunning) {
					log.ErrorContext(rootCtx, "startup scan failed", slog.String("error", err.Error()))
				}
			}()
		}
	}

	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- httpServer.Start() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		log.Info("shutdown signal received")
	case err := <-serverErrCh:
		if err != nil {
			log.Error("http server stopped", slog.String("error", err.Error()))
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown error", slog.String("error", err.Error()))
	}

	if sched != nil {
		stopCtx := sched.Stop()
		select {
		case <-stopCtx.Done():
		case <-time.After(5 * time.Second):
		}
	}

	grace := cfg.Scan.ShutdownGrace
	if grace <= 0 {
		grace = 60 * time.Second
	}
	waitForScans(rootCtx, scanUC, scanRepo, log, grace)
	rootCancel()

	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	log.Info("seasonfill stopped cleanly")
	return nil
}

func waitForScans(ctx context.Context, uc *scan.UseCase, repo *repositories.ScanRepository, log *slog.Logger, grace time.Duration) {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !uc.IsAnyRunning() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !uc.IsAnyRunning() {
		return
	}
	log.Warn("scans still in flight after grace, marking aborted")
	for inst, id := range uc.InflightScans() {
		if err := repo.MarkAborted(ctx, id, "shutdown grace exceeded"); err != nil {
			log.Error("mark aborted failed",
				slog.String("instance", inst),
				slog.String("scan_id", id.String()),
				slog.String("error", err.Error()),
			)
		}
	}
}
