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
	infraoidc "github.com/alexmorbo/seasonfill/infrastructure/oidc"
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
	if len(os.Args) > 1 && os.Args[1] == "auth-mode" {
		if err := runAuthMode(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "auth-mode: %v\n", err)
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
		if err := runtimeRepo.Upsert(bgCtx, runtime.Defaults(), nil); err != nil {
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
	oidcCache := infraoidc.NewProviderCache()
	oidcUC := authapp.NewOIDCLoginUseCase(oidcCache, adminRepo)
	if err := authapp.Bootstrap(bgCtx, adminRepo, authapp.BootstrapConfig{
		WebUser:         bootCfg.Auth.WebUser,
		WebPassword:     bootCfg.Auth.WebPassword,
		WebPasswordHash: bootCfg.Auth.WebPasswordHash,
	}, log); err != nil {
		return nil, fmt.Errorf("auth bootstrap: %w", err)
	}

	// cfg now reads from snap instead of bootstrap config
	authCfg := config.Auth{
		Enabled:          true,
		APIKey:           masterKey,
		SessionTTL:       snap.Auth.SessionTTL,
		SecureCookie:     snap.Auth.SecureCookie,
		TrustedProxies:   snap.Auth.TrustedProxies,
		OIDCClientSecret: bootCfg.Auth.OIDCClientSecret,
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
	for _, sc := range cfg.SonarrInstances {
		sonarrClientsByName[sc.Name] = clientFactory(sc)
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
	scanUC := scan.NewUseCase(scanInstances, evaluator, scanRepo, log, cfg.DryRun).
		WithGrabUseCase(grabUC).
		WithCooldowns(cooldownRepo).
		WithOrigins(originRepo).
		WithHealthRegistry(checker.Registry()).
		WithWaitGroup(&bgWG)
	rescanUC := rescan.NewUseCase(decisionRepo, grabRepo, scanRepo, scanUC, evaluator, holder.load, log)

	// 032e: per-instance webhook cooldown lookup reads live from the
	// instanceMapHolder so PUT /instances/<name> mutations to
	// cooldown.guid_failed_import_sec take effect on the next webhook
	// without a pod restart. The OnApplied fan-out swap-replaces the
	// holder map on every publish; this closure reflects whichever
	// snapshot is current at call time. Unknown instances → 0 (same
	// behaviour as pre-032e: log + skip the cooldown write).
	webhookUC := webhookuc.New(webhookuc.Deps{
		Grabs:     grabRepo,
		Cooldowns: cooldownRepo,
		Tx:        txr,
		GUIDCooldownLookup: func(name string) time.Duration {
			inst, ok := holder.load()[name]
			if !ok {
				return 0
			}
			return inst.Config.Cooldown.GUIDAfterFailedImport
		},
		Logger: log,
	})

	loginLimiter := authapp.NewIPLimiter(authapp.LoginLimit(), 5)
	webhookLimiter := authapp.NewIPLimiter(authapp.WebhookLimit(), 60)
	// Single registry value the reload bus drives via instanceMapHolder.
	// holder.load is invoked per-request by InstancesHandler /
	// GrabHandler / WebhookHandler — they see every Sonarr added or
	// removed via Settings UI without a pod restart.
	instanceReg := handlers.InstanceRegistry{Load: holder.load}

	instanceUC := instance.New(instanceRepo, runtimeRepo, cipher, bus, log)
	instanceCRUDHandler := handlers.NewInstanceCRUDHandler(instanceUC, log)
	probeClient := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  5 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
		},
	}
	instanceProbeHandler := handlers.NewInstanceProbeHandler(probeClient, log)

	httpServer := httpserver.NewServer(cfg.HTTP, scanUC, webhookUC,
		checker, scanRepo, decisionRepo, grabRepo,
		adminRepo, loginLimiter, webhookLimiter,
		instanceReg,
		cooldownRepo, grabUC, rescanUC,
		instanceCRUDHandler, instanceProbeHandler, runtimeConfigHandler, oidcUC, log)

	// Cooldown sweep loop — removes expired rows so the table stays
	// bounded. Cadence is reload-aware: the OnApplied fan-out calls
	// SetInterval whenever a new snapshot publishes a different
	// Scan.CooldownSweep, so changes via the runtime config UI take
	// effect without a pod restart.
	sweepInterval := cfg.Scan.CooldownSweep
	if sweepInterval <= 0 {
		sweepInterval = 15 * time.Minute
	}
	sweeper := newSweepLoop(cooldownRepo, sweepInterval, log)
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		sweeper.Run(rootCtx)
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

	subSched, subClients, err := startSubscribers(rootCtx, &bgWG, bus, log,
		bootScheduler, scanUC, sonarrClientsByName,
		clientFactory, checker, wd, holder, sweeper,
		&globalLimiterPtr, snap.GlobalRateLimit, authRuntimePtr, httpServer.Engine())
	if err != nil {
		return nil, fmt.Errorf("start subscribers: %w", err)
	}

	oidcProviderSub := reload.NewOIDCProviderSubscriber(oidcCache, log)
	go oidcProviderSub.Run(rootCtx, bus, func() {})

	// Re-publish the boot snapshot now that subscribers are alive
	// — they all apply it once and increment their success metric.
	bus.Publish(rootCtx, snap)

	// notifyTestContext fires testContextHook (integration builds only) so
	// E2E tests can assert per-subscriber state. The call is a no-op in
	// production builds (testcontext_stub.go provides the empty function).
	notifyTestContext(bus, subSched, subClients, authRuntimePtr, &globalLimiterPtr, holder.load, checker.Snapshot)

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

// runCooldownSweep is preserved for callers (and tests) that drive the
// sweep with a fixed cadence. New call sites should construct a
// sweepLoop directly so the cadence can be updated by the reload bus.
func runCooldownSweep(ctx context.Context, repo ports.CooldownRepository, every time.Duration, log *slog.Logger) {
	newSweepLoop(repo, every, log).Run(ctx)
}
