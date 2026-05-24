package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	authapp "github.com/alexmorbo/seasonfill/application/auth"
	"github.com/alexmorbo/seasonfill/application/bootstrap"
	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/instance"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/application/scan"
	webhookuc "github.com/alexmorbo/seasonfill/application/webhook"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/infrastructure/watchdog"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	httpserver "github.com/alexmorbo/seasonfill/interface/http"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		if err := runResetPassword(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "reset-password: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	bootCfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("bootstrap config: %w", err)
	}

	log := logger.New(logger.Config{
		Level:  bootCfg.Log.Level,
		Format: bootCfg.Log.Format,
		Output: os.Stdout,
	})
	slog.SetDefault(log)
	log.Info("starting seasonfill (bootstrap config)",
		slog.String("driver", bootCfg.Database.Driver))

	db, err := database.Open(bootCfg.Database)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	runtimeRepo := repositories.NewRuntimeConfigRepository(db)
	instanceRepo := repositories.NewSonarrInstanceRepository(db)

	ctx := context.Background()

	masterKey, err := bootstrap.ResolveAPIKey(ctx, bootCfg.Auth.APIKey, runtimeRepo, log)
	if err != nil {
		return fmt.Errorf("resolve api key: %w", err)
	}
	cipher, err := crypto.New(masterKey)
	if err != nil {
		return fmt.Errorf("derive cipher: %w", err)
	}

	// Seed runtime_config from Defaults() on a truly-fresh install.
	row, err := runtimeRepo.Get(ctx)
	switch {
	case err == nil:
		// happy path
	case errors.Is(err, ports.ErrNotFound):
		if err := runtimeRepo.Upsert(ctx, runtime.Defaults()); err != nil {
			return fmt.Errorf("seed runtime_config: %w", err)
		}
		row, err = runtimeRepo.Get(ctx)
		if err != nil {
			return fmt.Errorf("reload runtime_config after seed: %w", err)
		}
	default:
		return fmt.Errorf("read runtime_config: %w", err)
	}

	instances, err := instanceRepo.List(ctx, cipher)
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
	}
	for i := range instances {
		runtime.ApplyInstanceDefaults(&instances[i])
	}
	runtime.SortInstances(instances)

	snap := runtime.Snapshot{
		Cron: row.Cron, Scan: row.Scan, DryRun: row.DryRun,
		GlobalRateLimit: row.GlobalRateLimit, Auth: row.Auth,
		Instances: instances,
	}

	bus := runtime.NewBus(log)
	defer bus.Close()
	bus.Publish(ctx, snap)

	adminRepo := repositories.NewAdminUserRepository(db)
	if err := authapp.Bootstrap(ctx, adminRepo, authapp.BootstrapConfig{
		WebUser:         bootCfg.Auth.WebUser,
		WebPassword:     bootCfg.Auth.WebPassword,
		WebPasswordHash: bootCfg.Auth.WebPasswordHash,
	}, log); err != nil {
		return fmt.Errorf("auth bootstrap: %w", err)
	}

	// cfg now reads from snap instead of bootstrap config
	authCfg := config.Auth{
		Enabled:        true,
		APIKey:         masterKey,
		SessionTTL:     snap.Auth.SessionTTL,
		SecureCookie:   snap.Auth.SecureCookie,
		TrustedProxies: snap.Auth.TrustedProxies,
	}
	cfg := struct {
		HTTP            config.HTTPConfig
		SonarrInstances []runtime.InstanceSnapshot
		DryRun          bool
		GlobalRateLimit runtime.RateLimitSnapshot
		Scan            runtime.ScanSnapshot
		Cron            runtime.CronSnapshot
		Auth            config.Auth
	}{
		HTTP:            bootCfg.HTTP,
		SonarrInstances: instances,
		DryRun:          snap.DryRun,
		GlobalRateLimit: snap.GlobalRateLimit,
		Scan:            snap.Scan,
		Cron:            snap.Cron,
		Auth:            authCfg,
	}

	scanRepo := repositories.NewScanRepository(db)
	decisionRepo := repositories.NewDecisionRepository(db)
	grabRepo := repositories.NewGrabRepository(db)
	cooldownRepo := repositories.NewCooldownRepository(db)
	originRepo := repositories.NewOriginReleaseRepository(db)

	// Single shared global limiter (PRD §8.1). Nil = unlimited (N-new-1).
	// Observer wires the PRD §9.2 throttle counter at scope="global"; the
	// limiter itself stays metric-free (dependency rule).
	globalLimiter := ratelimit.NewFromRPMWithOptions(
		cfg.GlobalRateLimit.RPM,
		cfg.GlobalRateLimit.Burst,
		ratelimit.WithObserver("global", func(scope string) {
			observability.IncRateLimitThrottled("", scope)
		}),
	)

	scanInstances := make([]scan.Instance, 0, len(cfg.SonarrInstances))
	sonarrClients := make([]ports.SonarrClient, 0, len(cfg.SonarrInstances))
	sonarrClientsByName := make(map[string]ports.SonarrClient, len(cfg.SonarrInstances))
	scanInstancesByName := make(map[string]scan.Instance, len(cfg.SonarrInstances))
	cfgByName := make(map[string]config.HealthCheckConfig, len(cfg.SonarrInstances))
	for _, sc := range cfg.SonarrInstances {
		// Per-instance observer binds the instance name to the closure so the
		// limiter can stay instance-agnostic.
		instanceName := sc.Name
		instLimiter := ratelimit.NewFromRPMWithOptions(
			sc.RateLimit.RPM,
			sc.RateLimit.Burst,
			ratelimit.WithObserver("per_instance", func(scope string) {
				observability.IncRateLimitThrottled(instanceName, scope)
			}),
		)
		c := sonarr.NewWithOptions(sc.Name, sc.URL, sc.APIKey, sc.Timeout, instLimiter, log,
			sonarr.WithGlobalLimiter(globalLimiter),
			sonarr.WithSearchTimeout(sc.SearchTimeout),
		)
		sonarrClients = append(sonarrClients, c)
		sonarrClientsByName[sc.Name] = c
		si := scan.Instance{Config: sc, Client: c}
		scanInstances = append(scanInstances, si)
		scanInstancesByName[sc.Name] = si
		cfgByName[sc.Name] = config.NewHealthCheckConfig(sc.HealthCheck)
	}

	checker := healthcheck.New(db, sonarrClients)

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// M-9: track every background goroutine so we can wait for them to exit
	// before closing the DB handle below.
	var bgWG sync.WaitGroup

	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		checker.Run(rootCtx, 30*time.Second)
	}()

	// Watchdog rechecks Unavailable* instances at per-state cadences (D-2.3).
	wd := watchdog.New(checker.Registry(), checker, log, cfgByName)
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		wd.Run(rootCtx)
	}()

	txr := repositories.NewGormTransactor(db)
	evaluator := evaluate.NewPerInstanceUseCase(decisionRepo, log)
	grabUC := grab.NewUseCase(grabRepo, cooldownRepo, originRepo, sonarr.Classifier{}, log).
		WithTransactor(txr)
	rescanUC := rescan.NewUseCase(decisionRepo, grabRepo, evaluator,
		scanInstancesByName, log)
	scanUC := scan.NewUseCase(scanInstances, evaluator, scanRepo, log, cfg.DryRun).
		WithGrabUseCase(grabUC).
		WithCooldowns(cooldownRepo).
		WithOrigins(originRepo).
		WithHealthRegistry(checker.Registry()).
		WithWaitGroup(&bgWG)

	// 008c-#4: per-instance webhook cooldown lookup. Wire-time
	// construction of a closure that returns 0 for unknown instances.
	// ApplyInstanceDefaults guarantees a 48h floor on each configured
	// instance when YAML omits `guid_after_failed_import`. Closure
	// (not map) keeps internal state immutable.
	guidCooldownByInstance := make(map[string]time.Duration, len(cfg.SonarrInstances))
	for _, sc := range cfg.SonarrInstances {
		guidCooldownByInstance[sc.Name] = sc.Cooldown.GUIDAfterFailedImport
	}
	webhookUC := webhookuc.New(webhookuc.Deps{
		Grabs:     grabRepo,
		Cooldowns: cooldownRepo,
		Tx:        txr,
		GUIDCooldownLookup: func(instance string) time.Duration {
			return guidCooldownByInstance[instance]
		},
		Logger: log,
	})

	loginLimiter := authapp.NewIPLimiter(authapp.LoginLimit(), 5)
	webhookLimiter := authapp.NewIPLimiter(authapp.WebhookLimit(), 60)
	knownInstances := make(map[string]struct{}, len(cfg.SonarrInstances))
	for _, sc := range cfg.SonarrInstances {
		knownInstances[sc.Name] = struct{}{}
	}

	instanceUC := instance.New(instanceRepo, runtimeRepo, cipher, bus, log)
	instanceCRUDHandler := handlers.NewInstanceCRUDHandler(instanceUC, log)
	instanceProbeHandler := handlers.NewInstanceProbeHandler(&http.Client{}, log)

	httpServer := httpserver.NewServer(cfg.HTTP, scanUC, webhookUC,
		checker, scanRepo, decisionRepo, grabRepo,
		adminRepo, loginLimiter, webhookLimiter,
		sonarrClientsByName, handlers.BuildModeMap(cfg.SonarrInstances),
		knownInstances,
		cooldownRepo, grabUC, rescanUC, scanInstancesByName, instanceCRUDHandler, instanceProbeHandler, log)

	// Cooldown sweep ticker — removes expired rows so the table stays bounded.
	sweepInterval := cfg.Scan.CooldownSweep
	if sweepInterval <= 0 {
		sweepInterval = 15 * time.Minute
	}
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		runCooldownSweep(rootCtx, cooldownRepo, sweepInterval, log)
	}()

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

	// M-9: drain background goroutines before closing the DB handle.
	drainBackground(&bgWG, 10*time.Second, log)

	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	log.Info("seasonfill stopped cleanly")
	return nil
}

func runCooldownSweep(ctx context.Context, repo ports.CooldownRepository, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := repo.Sweep(ctx, time.Now().UTC())
			if err != nil {
				log.ErrorContext(ctx, "cooldown sweep failed", slog.String("error", err.Error()))
				continue
			}
			if n > 0 {
				log.DebugContext(ctx, "cooldown sweep removed expired rows", slog.Int64("rows", n))
			}
		}
	}
}
