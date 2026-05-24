package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	authapp "github.com/alexmorbo/seasonfill/application/auth"
	"github.com/alexmorbo/seasonfill/application/bootstrap"
	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/instance"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/application/runtimeconfig"
	"github.com/alexmorbo/seasonfill/application/scan"
	webhookuc "github.com/alexmorbo/seasonfill/application/webhook"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/infrastructure/watchdog"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	httpserver "github.com/alexmorbo/seasonfill/interface/http"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/netguard"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	_, err := runWithContext(ctx, nil)
	return err
}

// runForTest is a test-only entry point (gated by //go:build integration).
// It runs the full server lifecycle against ctx, blocking until ctx is
// cancelled. onReady is called with the live bus after all six subscribers
// are registered, allowing the test to start interacting while the server
// is still running.
func runForTest(ctx context.Context, onReady func(*runtime.Bus)) error {
	_, err := runWithContext(ctx, onReady)
	return err
}

// runWithContext contains the full server lifecycle. onReady, if non-nil,
// is called with the live bus after all six subscribers are registered.
func runWithContext(ctx context.Context, onReady func(*runtime.Bus)) (*runtime.Bus, error) {
	bootCfg, err := config.FromEnv()
	if err != nil {
		return nil, fmt.Errorf("bootstrap config: %w", err)
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
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := database.Migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	runtimeRepo := repositories.NewRuntimeConfigRepository(db)
	instanceRepo := repositories.NewSonarrInstanceRepository(db)

	bgCtx := context.Background()

	masterKey, err := bootstrap.ResolveAPIKey(bgCtx, bootCfg.Auth.APIKey, runtimeRepo, log)
	if err != nil {
		return nil, fmt.Errorf("resolve api key: %w", err)
	}
	cipher, err := crypto.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("derive cipher: %w", err)
	}

	// Seed runtime_config from Defaults() on a truly-fresh install.
	row, err := runtimeRepo.Get(bgCtx)
	switch {
	case err == nil:
		// happy path
	case errors.Is(err, ports.ErrNotFound):
		if err := runtimeRepo.Upsert(bgCtx, runtime.Defaults()); err != nil {
			return nil, fmt.Errorf("seed runtime_config: %w", err)
		}
		row, err = runtimeRepo.Get(bgCtx)
		if err != nil {
			return nil, fmt.Errorf("reload runtime_config after seed: %w", err)
		}
	default:
		return nil, fmt.Errorf("read runtime_config: %w", err)
	}

	instances, err := instanceRepo.List(bgCtx, cipher)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
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

	runtimeConfigUC := runtimeconfig.New(runtimeRepo, instanceRepo, cipher, bus, log)
	runtimeConfigHandler := handlers.NewRuntimeConfigHandler(runtimeConfigUC, log)

	adminRepo := repositories.NewAdminUserRepository(db)
	if err := authapp.Bootstrap(bgCtx, adminRepo, authapp.BootstrapConfig{
		WebUser:         bootCfg.Auth.WebUser,
		WebPassword:     bootCfg.Auth.WebPassword,
		WebPasswordHash: bootCfg.Auth.WebPasswordHash,
	}, log); err != nil {
		return nil, fmt.Errorf("auth bootstrap: %w", err)
	}

	// cfg now reads from snap instead of bootstrap config
	authCfg := config.Auth{
		Enabled:        true,
		APIKey:         masterKey,
		SessionTTL:     snap.Auth.SessionTTL,
		SecureCookie:   snap.Auth.SecureCookie,
		TrustedProxies: snap.Auth.TrustedProxies,
	}
	httpCfg := bootCfg.HTTP
	httpCfg.Auth = authCfg
	cfg := struct {
		HTTP            config.HTTPConfig
		SonarrInstances []runtime.InstanceSnapshot
		DryRun          bool
		GlobalRateLimit runtime.RateLimitSnapshot
		Scan            runtime.ScanSnapshot
		Cron            runtime.CronSnapshot
	}{
		HTTP:            httpCfg,
		SonarrInstances: instances,
		DryRun:          snap.DryRun,
		GlobalRateLimit: snap.GlobalRateLimit,
		Scan:            snap.Scan,
		Cron:            snap.Cron,
	}

	scanRepo := repositories.NewScanRepository(db)
	decisionRepo := repositories.NewDecisionRepository(db)
	grabRepo := repositories.NewGrabRepository(db)
	cooldownRepo := repositories.NewCooldownRepository(db)
	originRepo := repositories.NewOriginReleaseRepository(db)

	// Single shared global limiter pointer (live-reloaded). Seed from the
	// boot snapshot so the first publish's subscriber diff-skip works.
	var globalLimiterPtr atomic.Pointer[ratelimit.Limiter]
	globalLimiterPtr.Store(reload.DefaultGlobalLimiterFactory(
		cfg.GlobalRateLimit.RPM, cfg.GlobalRateLimit.Burst))

	clientFactory := buildSonarrClientFactory(&globalLimiterPtr, log)
	sonarrClientsByName := make(map[string]ports.SonarrClient, len(cfg.SonarrInstances))
	bootCfgs := make(map[string]runtime.InstanceSnapshot, len(cfg.SonarrInstances))
	for _, sc := range cfg.SonarrInstances {
		c := clientFactory(sc)
		sonarrClientsByName[sc.Name] = c
		bootCfgs[sc.Name] = sc
	}
	sonarrClients := make([]ports.SonarrClient, 0, len(sonarrClientsByName))
	scanInstances := make([]scan.Instance, 0, len(sonarrClientsByName))
	scanInstancesByName := make(map[string]scan.Instance, len(sonarrClientsByName))
	cfgByName := make(map[string]config.HealthCheckConfig, len(sonarrClientsByName))
	for _, sc := range cfg.SonarrInstances {
		c := sonarrClientsByName[sc.Name]
		sonarrClients = append(sonarrClients, c)
		si := scan.Instance{Config: sc, Client: c}
		scanInstances = append(scanInstances, si)
		scanInstancesByName[sc.Name] = si
		cfgByName[sc.Name] = config.NewHealthCheckConfig(sc.HealthCheck)
	}
	holder := newInstanceMapHolder(scanInstancesByName)

	// Registry is constructed ONCE here. checker.Registry() returns a
	// stable pointer for the life of the process; the reload subscriber
	// mutates membership via ReplaceClients, NOT by replacing the pointer.
	checker := healthcheck.New(db, sonarrClients)

	rootCtx, rootCancel := context.WithCancel(ctx)
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
	rescanUC := rescan.NewUseCase(decisionRepo, grabRepo, evaluator, holder.load, log)
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
	probeClient := &http.Client{
		// One-shot probe MUST NOT follow redirects — a 3xx is a signal that
		// the URL is not Sonarr's API root. http.ErrUseLastResponse keeps the
		// response object intact so the handler can render a clean reason.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			// Reject RFC1918/loopback/link-local/ULA after DNS resolution
			// (defeats rebinding by inspecting the bound IP).
			DialContext: (&net.Dialer{
				Control: netguard.BlockPrivate,
				Timeout: 5 * time.Second,
			}).DialContext,
		},
	}
	instanceProbeHandler := handlers.NewInstanceProbeHandler(probeClient, log)

	httpServer := httpserver.NewServer(cfg.HTTP, scanUC, webhookUC,
		checker, scanRepo, decisionRepo, grabRepo,
		adminRepo, loginLimiter, webhookLimiter,
		sonarrClientsByName, handlers.BuildModeMap(cfg.SonarrInstances),
		knownInstances,
		cooldownRepo, grabUC, rescanUC, scanInstancesByName, instanceCRUDHandler, instanceProbeHandler, runtimeConfigHandler, log)

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

	// Build the boot scheduler (if cron is enabled) so the
	// subscriber starts in the same state as the snapshot.
	var bootScheduler *scheduler.Scheduler
	if cfg.Cron.Enabled {
		bootScheduler = scheduler.New(cfg.Cron.Schedule, cfg.Cron.Jitter, log)
		if err := bootScheduler.Start(rootCtx, scanUC); err != nil {
			return nil, fmt.Errorf("start scheduler: %w", err)
		}
		if cfg.Cron.OnStart {
			go func() {
				if _, err := scanUC.Run(rootCtx, scan.TriggerStartup); err != nil && !errors.Is(err, scan.ErrScanAlreadyRunning) {
					log.ErrorContext(rootCtx, "startup scan failed", slog.String("error", err.Error()))
				}
			}()
		}
	}

	// Pull the AuthRuntime pointer out of the http server's auth
	// handler so we can hand it to the reload subscriber.
	authHandler := httpServer.AuthHandler()
	var authRuntimePtr *middleware.AuthRuntimePointer
	if authHandler != nil {
		authRuntimePtr = authHandler.AuthRuntime()
	}

	subSched, _, err := startSubscribers(rootCtx, &bgWG, bus, log,
		bootScheduler, scanUC, sonarrClientsByName, bootCfgs,
		clientFactory, checker, holder,
		&globalLimiterPtr, authRuntimePtr, httpServer.Engine())
	if err != nil {
		return nil, fmt.Errorf("start subscribers: %w", err)
	}

	// Re-publish the boot snapshot now that subscribers are alive
	// — they all apply it once and increment their success metric.
	bus.Publish(rootCtx, snap)

	// Notify caller (test helper) that the bus is ready. Placed AFTER
	// startSubscribers + boot Publish so the test can assert counters
	// immediately without polling.
	if onReady != nil {
		onReady(bus)
	}

	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- httpServer.Start() }()

	select {
	case <-ctx.Done():
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

	if cur := subSched.Current(); cur != nil {
		stopCtx := cur.Stop()
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
	return bus, nil
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
