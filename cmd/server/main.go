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
	appenrich "github.com/alexmorbo/seasonfill/application/enrichment"
	"github.com/alexmorbo/seasonfill/application/evaluate"
	appextsvc "github.com/alexmorbo/seasonfill/application/externalservices"
	"github.com/alexmorbo/seasonfill/application/gc"
	"github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/instance"
	apppeople "github.com/alexmorbo/seasonfill/application/people"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/application/runtimeconfig"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/application/seriesrefresh"
	"github.com/alexmorbo/seasonfill/application/torrentsync"
	webhookuc "github.com/alexmorbo/seasonfill/application/webhook"
	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	dompeople "github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	infraextsvc "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
	"github.com/alexmorbo/seasonfill/infrastructure/mediastore"
	infraoidc "github.com/alexmorbo/seasonfill/infrastructure/oidc"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	infraregrab "github.com/alexmorbo/seasonfill/infrastructure/regrab"
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
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/runtime/tz"
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
	if len(os.Args) > 1 && os.Args[1] == "grabs" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: seasonfill grabs <reparse> [flags]")
			os.Exit(2)
		}
		switch os.Args[2] {
		case "reparse":
			if err := runReparseCLI(context.Background(), os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "reparse: %v\n", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown grabs subcommand: %s\n", os.Args[2])
			os.Exit(2)
		}
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

	instanceRepo := repositories.NewSonarrInstanceRepository(db)

	bgCtx := context.Background()

	// We need a temporary repo without cipher to resolve the API key first.
	// Then we rebuild the repo with the derived cipher.
	tempRuntimeRepo := repositories.NewRuntimeConfigRepository(db, nil)
	masterKey, err := bootstrap.ResolveAPIKey(bgCtx, bootCfg.Auth.APIKey, tempRuntimeRepo, log)
	if err != nil {
		return nil, fmt.Errorf("resolve api key: %w", err)
	}
	cipher, err := crypto.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("derive cipher: %w", err)
	}
	runtimeRepo := repositories.NewRuntimeConfigRepository(db, cipher)

	// Story 301: app-level settings (id=1) — currently only the
	// operator-selected timezone. Built early so the scheduler
	// factory and the HTTP handler share the same Resolver. The
	// store is the GORM-backed app_settings repo; the v36 seed
	// guarantees a singleton row exists.
	appSettingsRepo := repositories.NewAppSettingsRepository(db)
	tzResolver := tz.New(bgCtx, appSettingsRepo, log)
	log.Info("timezone resolver",
		slog.String("name", tzResolver.Name()),
		slog.String("source", string(tzResolver.Source())))
	timezoneHandler := handlers.NewTimezoneHandler(tzResolver, log)

	// Story 305: generic DB-backed rate-limit counter. Currently
	// consumed by the OMDb budget guard (replaces the in-process
	// counter that zeroed on every pod restart). Other external-
	// service clients can opt in by injecting `quotaCounter` and
	// switching their guard to the QuotaCounter port.
	quotaCounter := repositories.NewQuotaCounterRepository(db)

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

	runtimeConfigUC := runtimeconfig.New(runtimeRepo, instanceRepo, cipher, bus, log).
		WithClientSecretEnv(bootCfg.Auth.OIDCClientSecret)
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
	seriesRepo := repositories.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	counterRepo := repositories.NewCounterRepository(db)
	scanUC := scan.NewUseCase(scanInstances, evaluator, scanRepo, log, cfg.DryRun).
		WithGrabUseCase(grabUC).
		WithCooldowns(cooldownRepo).
		WithOrigins(originRepo).
		WithSeriesCache(seriesCacheRepo).
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
	// Story 218 (E-2): webhook SeriesDelete cascade soft-deletes
	// episode_states under the deleted series. Repo is constructed
	// here so the cascade port is wired at boot.
	webhookEpisodeStatesRepo := repositories.NewEpisodeStatesRepository(db)
	// 221 (A-3) — torrent_series_map repo wired here so the webhook
	// path can write the bridge row in the same tx as the
	// grab_records.torrent_hash update. Repo also feeds the
	// torrentsync reconciler constructed below.
	torrentSeriesMapRepo := repositories.NewTorrentSeriesMapRepository(db)

	// Story 300 (E-1 wiring fix) — construct scan.Syncer so the
	// webhook SeriesAdd path populates the canonical entity model
	// (series + episodes + episode_states + series_genres +
	// series_networks) instead of falling back to the thin
	// CacheEntry write. Repos are stateless GORM wrappers (same
	// shape as the Story 215 seriesdetail block below), so re-
	// constructing them here is free. Lookup returns the concrete
	// *sonarr.Client because Syncer.SyncFromSonarrAPI needs the
	// payload-fetcher methods (GetSeriesPayload / ListEpisodesForSync
	// / ListEpisodeFilesForSync) that live on the concrete type,
	// not on ports.SonarrClient. Unknown instance OR a non-concrete
	// client → (nil, false), webhook silently falls back to the
	// pre-E-1 thin CacheEntry path (same degradation pattern as the
	// existing SonarrClientFor / InstanceFor closures below).
	webhookEpisodesRepo := repositories.NewEpisodesRepository(db)
	webhookEpisodeTextsRepo := repositories.NewEpisodeTextsRepository(db)
	webhookGenresRepo := repositories.NewGenresRepository(db)
	webhookGenresI18nRepo := repositories.NewGenresI18nRepository(db)
	webhookNetworksRepo := repositories.NewNetworksRepository(db)
	webhookSeriesSyncer := &scan.Syncer{
		Deps: scan.SyncDeps{
			Series:        seriesRepo,
			SeriesCache:   seriesCacheRepo,
			Episodes:      webhookEpisodesRepo,
			EpisodeStates: webhookEpisodeStatesRepo,
			EpisodeTexts:  webhookEpisodeTextsRepo,
			Genres:        scan.NewGenresAdapter(webhookGenresRepo, webhookGenresI18nRepo),
			Networks:      scan.NewNetworksAdapter(webhookNetworksRepo),
			Logger:        log,
		},
		Lookup: func(name string) (*sonarr.Client, bool) {
			h := holder.load()
			if h == nil {
				return nil, false
			}
			inst, ok := h[name]
			if !ok || inst.Client == nil {
				return nil, false
			}
			concrete, ok := inst.Client.(*sonarr.Client)
			if !ok {
				return nil, false
			}
			return concrete, true
		},
		Logger: log,
	}

	webhookUC := webhookuc.New(webhookuc.Deps{
		Grabs:            grabRepo,
		Cooldowns:        cooldownRepo,
		SeriesCache:      seriesCacheRepo,
		Tx:               txr,
		EpisodeStates:    webhookEpisodeStatesRepo,
		TorrentSeriesMap: torrentSeriesMapRepo,
		SeriesSyncer:     webhookSeriesSyncer,
		GUIDCooldownLookup: func(name string) time.Duration {
			inst, ok := holder.load()[name]
			if !ok {
				return 0
			}
			return inst.Config.Cooldown.GUIDAfterFailedImport
		},
		Logger: log,
		SonarrClientFor: func(name string) (ports.SonarrClient, bool) {
			if h := holder.load(); h != nil {
				if inst, ok := h[name]; ok && inst.Client != nil {
					return inst.Client, true
				}
			}
			return nil, false
		},
		InstanceFor: func(name string) (runtime.InstanceSnapshot, bool) {
			if h := holder.load(); h != nil {
				if inst, ok := h[name]; ok {
					return inst.Config, true
				}
			}
			return runtime.InstanceSnapshot{}, false
		},
	})

	loginLimiter := authapp.NewIPLimiter(authapp.LoginLimit(), 5)
	webhookLimiter := authapp.NewIPLimiter(authapp.WebhookLimit(), 60)
	// Single registry value the reload bus drives via instanceMapHolder.
	// holder.load is invoked per-request by InstancesHandler /
	// GrabHandler / WebhookHandler — they see every Sonarr added or
	// removed via Settings UI without a pod restart.
	instanceReg := handlers.InstanceRegistry{Load: holder.load}

	webhookStatusCache := webhookinstall.NewStatusCache()
	webhookReconciler := webhookinstall.New(webhookinstall.Deps{
		Lookup:    webhookReconcileLookup(instanceReg),
		PublicURL: webhookinstall.PublicURLFromContext,
		Cache:     webhookStatusCache,
		APIKey:    cfg.HTTP.Auth.APIKey,
		Logger:    log,
	})

	instanceUC := instance.New(instanceRepo, runtimeRepo, cipher, bus, log).
		WithWebhookReconciler(reconcilerAdapter{inner: webhookReconciler}).
		WithWebhookStatusCache(webhookStatusCache)
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

	// Phase 10 Watchdog. The settings CRUD is wired here; the regrab
	// orchestrator + per-instance polling loop + reload-bus fanout are
	// constructed below and threaded through startSubscribers.
	qbitSettingsRepo := repositories.NewQbitSettingsRepository(db)
	qbitSettingsUC := regrab.NewSettingsUseCase(qbitSettingsRepo, instanceRepo, cipher, log).
		WithWebhookChecker(newWebhookChecker(instanceReg))
	qbitSettingsHandler := handlers.NewQbitSettingsHandler(qbitSettingsUC, log)

	// regrab orchestrator — depends on the settings use case (Lookup),
	// the instance registry (Get), the qBit + detector factories, the
	// grab / cooldown / blacklist / counter repos, and the evaluator +
	// grab use case. Metrics adapter is the production VictoriaMetrics
	// implementation.
	blacklistRepo := repositories.NewWatchdogBlacklistRepository(db)
	noBetterCounterRepo := repositories.NewNoBetterCounterRepository(db)
	regrabUC := regrab.NewUseCase(
		qbitSettingsUC, // implements SettingsLookup
		regrabInstanceRegistry{reg: instanceReg},
		infraregrab.QbitClientFactoryFunc{},
		infraregrab.DetectorFactoryFunc{},
		grabRepo, cooldownRepo, blacklistRepo, noBetterCounterRepo,
		evaluator, grabUC,
		log,
	).WithMetrics(observability.WatchdogMetricsAdapter{}).
		WithDecisions(decisionRepo)

	// regrab loop owns the per-instance polling goroutines; SwapSettings
	// is called from the OnApplied fanout below.
	regrabLoopVal := newRegrabLoop(regrabUC, observability.WatchdogMetricsAdapter{}, &bgWG, log)
	regrabLoopVal.Start(rootCtx)

	// 220 (A-2) — torrentsync loop. Reuses the same qbitLoader as
	// regrabLoop for reload publishes (one fetch of qbit settings
	// per applied snapshot is shared between the two loop owners).
	qbitTorrentsRepo := repositories.NewQbitTorrentsRepository(db)
	qbitTorrentEventsRepo := repositories.NewQbitTorrentEventsRepository(db)
	torrentsyncStore := torrentsync.NewStore()
	torrentsyncPolicy := torrentsync.NewPersistPolicy(
		qbitTorrentsRepo, qbitTorrentEventsRepo, log,
	)
	torrentsyncFactory := torrentsyncSessionFactoryAdapter{
		factory: infraregrab.QbitClientFactoryFunc{},
		lookup:  qbitSettingsUC,
	}
	// 221 (A-3) — reconciler (torrentSeriesMapRepo already
	// constructed above for the webhook same-tx write).
	// sonarrFor wires the per-instance Sonarr client lookup the
	// reconciler needs for sources 3 + 4. Production wiring reuses
	// the instance holder; the concrete *sonarr.Client satisfies
	// torrentsync.SonarrReconciler (its QueueAll + GrabHistoryPaged
	// are exactly the two methods in the port).
	sonarrFor := func(instance string) (torrentsync.SonarrReconciler, bool) {
		h := holder.load()
		inst, ok := h[instance]
		if !ok || inst.Client == nil {
			return nil, false
		}
		client, ok := inst.Client.(*sonarr.Client)
		if !ok {
			return nil, false
		}
		return client, true
	}
	reconciler := torrentsync.NewReconciler(
		torrentsyncStore,
		torrentSeriesMapRepo,
		grabRepo,
		sonarrFor,
		observability.TorrentsyncMetricsAdapter{},
		log,
	)
	torrentsyncUC := torrentsync.NewUseCase(
		torrentsyncStore, torrentsyncPolicy,
		torrentsyncFactory, qbitTorrentsRepo, log,
	).WithReconciler(reconciler)
	torrentsyncLoopVal := newTorrentsyncLoop(
		productionTorrentsyncRunner{uc: torrentsyncUC, logger: log},
		&bgWG, log,
	)
	torrentsyncLoopVal.Start(rootCtx)

	// 047a — watchdog rollup handler wiring.
	watchdogInstanceAdapter := watchdogInstanceLister{repo: instanceRepo, cipher: cipher}
	watchdogRollupHandler := handlers.NewWatchdogRollupHandler(
		qbitSettingsUC,          // SettingsLookup
		regrabUC,                // RollupSnapshotProvider
		grabRepo,                // rollupGrabCounter
		blacklistRepo,           // rollupBlacklistCounter
		watchdogInstanceAdapter, // InstanceLister
		watchdogInstanceAdapter, // InstanceIDLookup
		log,
	).WithQbitProbe(infraregrab.QbitProbeFunc{}).
		WithQbitTorrentsLister(infraregrab.QbitTorrentsListerFunc{})

	// 047b — blacklist handler + webhooks aggregate handler. blacklistRepo
	// and seriesCacheRepo are already constructed above (Phase 11 + 047a);
	// reuse them directly.
	watchdogBlacklistHandler := handlers.NewWatchdogBlacklistHandler(
		blacklistRepo,           // BlacklistPager (production repo satisfies the narrow interface)
		seriesCacheRepo,         // SeriesTitleResolver (production repo has Get(name, sonarrSeriesID))
		watchdogInstanceAdapter, // InstanceIDLookup — same adapter as 047a
		log,
	)

	// 098a — watchdog seasons aggregate read view. Joins the watchdog
	// source-of-truth tables (origin_releases, cooldowns, regrab_no_
	// better_counter, watchdog_blacklist) with series_cache so the SPA
	// can render the watched-seasons page without per-row fetches.
	watchdogSeasonsRepo := repositories.NewWatchdogSeasonsRepository(db)
	watchdogSeasonsHandler := handlers.NewWatchdogSeasonsHandler(
		watchdogSeasonsRepo,
		watchdogSeasonsRepo,
		qbitSettingsUC,
		log,
	)
	webhooksAggregateHandler := handlers.NewWebhooksAggregateHandler(
		webhookReconciler,
		watchdogInstanceAdapter, // InstanceLister
		log,
	)

	// qBit settings loader for the fanout — calls List + builds the
	// Settings map fresh on every publish. The Lookup closure delegates
	// to the settings use case so password decryption is centralised.
	qbitLoader := qbitSettingsLoaderFunc(func(ctx context.Context) map[string]regrab.Settings {
		recs, err := qbitSettingsRepo.List(ctx)
		if err != nil {
			log.WarnContext(ctx, "qbit_settings_list_failed",
				slog.String("error", err.Error()))
			return map[string]regrab.Settings{}
		}
		out := make(map[string]regrab.Settings, len(recs))
		instances, err := instanceRepo.List(ctx, cipher)
		if err != nil {
			log.WarnContext(ctx, "qbit_settings_list_instances_failed",
				slog.String("error", err.Error()))
			return map[string]regrab.Settings{}
		}
		byID := make(map[uint]string, len(instances))
		for _, inst := range instances {
			byID[inst.ID] = inst.Name
		}
		for _, rec := range recs {
			name := byID[rec.InstanceID]
			if name == "" {
				continue
			}
			s, err := regrab.NewSettingsFromRecord(rec, name, cipher)
			if err != nil {
				log.WarnContext(ctx, "qbit_settings_decrypt_failed",
					slog.String("instance", name),
					slog.String("error", err.Error()))
				continue
			}
			out[name] = s
		}
		return out
	})

	// Story 202 (S-2) — external services runtime config. The subscriber
	// is built first with a nil use case so it can be injected as the
	// use case's Publisher; the use case is then constructed and back-
	// wired via SetUseCase before Start is called below. Plaintext keys
	// never leave the subscriber/use case pair — the HTTP handler emits
	// the masked DTO only.
	extRepo := infraextsvc.NewRepository(db, cipher)
	extSub := NewExternalServicesSubscriber(bus, log)
	extUC := appextsvc.NewUseCase(extRepo, bootCfg.ExternalServices.Lookup(),
		appextsvc.NewRealTester(), extSub, log)
	extSub.SetUseCase(extUC)
	externalServicesHandler := handlers.NewExternalServicesHandler(extUC, log)

	// Story 214 (F-1) — media pipeline plumbing. mediastore is built
	// from the bootstrap MediaStore config (default mode=off → null
	// store, every op returns ErrNotSupported; the downloader treats
	// that as a soft fail and the HTTP handler 502s on lost-object,
	// which is the correct behaviour for an unconfigured deploy).
	// mediaAssetsRepo is shared between the downloader (via the
	// enrichment bundle) and the HTTP MediaHandler — one source of
	// truth for media_assets rows. The TMDB-proxied HTTP client is
	// constructed inside wireEnrichment and threaded back through
	// enrichBundle.MediaHTTP; until that handle is available the
	// MediaHandler falls back to http.DefaultClient (lost-object
	// recovery path only).
	mediaStoreImpl, err := mediastore.New(rootCtx, mediastore.Config{
		Mode: mediastore.Mode(bootCfg.MediaStore.Mode),
		S3: mediastore.S3Config{
			Endpoint:  bootCfg.MediaStore.S3.Endpoint,
			Bucket:    bootCfg.MediaStore.S3.Bucket,
			AccessKey: bootCfg.MediaStore.S3.AccessKey,
			SecretKey: bootCfg.MediaStore.S3.SecretKey,
			Region:    bootCfg.MediaStore.S3.Region,
			UseSSL:    bootCfg.MediaStore.S3.UseSSL,
		},
		FSPath: bootCfg.MediaStore.FSPath,
	})
	if err != nil {
		return nil, fmt.Errorf("mediastore: %w", err)
	}
	mediaAssetsRepo := repositories.NewMediaAssetsRepository(db)
	mediaHandler := handlers.NewMediaHandler(mediaStoreImpl, mediaAssetsRepo, nil, log)

	// Story 312: media resolver for the seriesdetail composer. nil-OK
	// `mediaAssetsRepo` falls back to a nop resolver inside NewMediaResolver
	// → every wire field stays nil and the frontend renders monograms.
	var mediaHashLookup seriesdetail.MediaHashLookupPort
	if mediaAssetsRepo != nil {
		mediaHashLookup = mediaAssetsRepo
	}
	seriesDetailMediaResolver := seriesdetail.NewMediaResolver(mediaHashLookup, log)

	// Story 215 (G-1) — series detail composer + handlers. The repos
	// are stateless GORM wrappers around `db`, so re-constructing
	// them here is free; the enrichment block below re-uses its own
	// instances of the same set for the worker pipeline.
	sdSeriesRepo := seriesRepo
	sdSeriesTextsRepo := repositories.NewSeriesTextsRepository(db)
	sdSeasonsRepo := repositories.NewSeasonsRepository(db)
	sdEpisodesRepo := repositories.NewEpisodesRepository(db)
	sdEpisodeStatesRepo := repositories.NewEpisodeStatesRepository(db)
	sdEpisodeTextsRepo := repositories.NewEpisodeTextsRepository(db)
	sdSeriesPeopleRepo := repositories.NewSeriesPeopleRepository(db)
	sdPeopleRepo := repositories.NewPeopleRepository(db)
	sdGenresRepo := repositories.NewGenresRepository(db)
	sdKeywordsRepo := repositories.NewKeywordsRepository(db)
	sdNetworksRepo := repositories.NewNetworksRepository(db)
	sdCompaniesRepo := repositories.NewCompaniesRepository(db)
	sdVideosRepo := repositories.NewVideosRepository(db)
	sdContentRatingsRepo := repositories.NewContentRatingsRepository(db)
	sdExternalIDsRepo := repositories.NewExternalIDsRepository(db)
	sdRecommendationsRepo := repositories.NewRecommendationsRepository(db)
	sdSyncLogRepo := repositories.NewSyncLogRepository(db)

	seriesDetailComposer := seriesdetail.NewComposer(seriesdetail.Deps{
		SeriesCache:       seriesCacheRepo,
		SeriesCacheLookup: seriesCacheRepo,
		Series:            sdSeriesRepo,
		SeriesTexts:       sdSeriesTextsRepo,
		Seasons:           sdSeasonsRepo,
		Episodes:          sdEpisodesRepo,
		EpisodeStates:     sdEpisodeStatesRepo,
		EpisodeTexts:      sdEpisodeTextsRepo,
		SeriesPeople:      sdSeriesPeopleRepo,
		People:            sdPeopleRepo,
		Genres:            sdGenresRepo,
		Keywords:          sdKeywordsRepo,
		Networks:          sdNetworksRepo,
		Companies:         sdCompaniesRepo,
		Videos:            sdVideosRepo,
		ContentRatings:    sdContentRatingsRepo,
		ExternalIDs:       sdExternalIDsRepo,
		Recommendations:   sdRecommendationsRepo,
		SyncLog:           sdSyncLogRepo,
		SonarrFor: func(name string) (seriesdetail.SonarrQueueLister, bool) {
			h := holder.load()
			if h == nil {
				return nil, false
			}
			inst, ok := h[name]
			if !ok || inst.Client == nil {
				return nil, false
			}
			concrete, ok := inst.Client.(*sonarr.Client)
			if !ok {
				return nil, false
			}
			return concrete, true
		},
		Logger:        log,
		MediaResolver: seriesDetailMediaResolver,
	})
	seriesDetailHandler := handlers.NewSeriesDetailHandler(seriesDetailComposer, log)
	seriesSeasonHandler := handlers.NewSeriesSeasonHandler(seriesDetailComposer, log)

	// Story 216 (H-1) — full cast & crew composer. Reuses the 215
	// repos (series_cache + series + series_people + people) plus
	// the new EpisodesRepository.CountBySeries method and a thin
	// adapter projecting repositories.PersonCredit → composer-local
	// PersonCreditRef.
	sdPersonCreditsRepo := repositories.NewPersonCreditsRepository(db)
	castComposer := seriesdetail.NewCastComposer(seriesdetail.CastDeps{
		SeriesCache:       seriesCacheRepo,
		SeriesCacheLookup: seriesCacheRepo,
		Series:            sdSeriesRepo,
		SeriesPeople:      sdSeriesPeopleRepo,
		People:            sdPeopleRepo,
		PersonCredits:     personCreditsAdapter{r: sdPersonCreditsRepo},
		EpisodesCount:     sdEpisodesRepo,
		Logger:            log,
		MediaResolver:     seriesDetailMediaResolver,
	})
	seriesCastHandler := handlers.NewSeriesCastHandler(castComposer, log)

	// Story 217 (H-2) — person detail use case. Adapter wraps
	// PeopleRepository so the application port distinguishes the
	// bio-skipping GetByTMDBID path (hot, used for the tmdb→id
	// resolution) from the bio-resolving GetWithBio path (cold,
	// used after id is known) — same repository, two narrow
	// methods. The Enqueuer is a late-binding holder; the real
	// dispatcher is wired in after wireEnrichment returns (the
	// holder's inner is nil-OK and the use case logs a warn line
	// when stub persons land before the dispatcher is up).
	peopleEnqueuerHolder := &personEnqueuerHolder{}
	peopleUC := apppeople.NewUseCase(apppeople.Deps{
		People:        peopleReaderAdapter{r: sdPeopleRepo},
		PersonCredits: personCreditsReaderAdapter{r: sdPersonCreditsRepo},
		SeriesByTMDB:  sdSeriesRepo,
		SeriesCache:   seriesCacheRepo,
		SyncLog:       sdSyncLogRepo,
		Enqueuer:      peopleEnqueuerHolder,
		Logger:        log,
	})
	peopleHandler := handlers.NewPeopleHandler(peopleUC, log)

	// Story 218 (E-2) — series refresh trigger. Reuses the
	// peopleEnqueuerHolder so the same late-binding dispatcher
	// satisfies both the H-2 use case AND the refresh path.
	seriesRefreshUC, err := seriesrefresh.New(seriesrefresh.Deps{
		SeriesCache:  seriesCacheRepo,
		Series:       seriesRefreshSeriesAdapter{r: seriesRepo},
		SeriesPeople: seriesRefreshCastAdapter{r: sdSeriesPeopleRepo},
		Dispatcher:   peopleEnqueuerHolder,
		Logger:       log,
	})
	if err != nil {
		return nil, fmt.Errorf("seriesrefresh use case: %w", err)
	}
	seriesRefreshHandler := handlers.NewSeriesRefreshHandler(seriesRefreshUC, log)

	// Story 222 (A-4) — per-series torrents endpoint. Reuses the
	// torrentsync store + qbit_torrents repo wired by 220/221.
	// torrentSeriesMapRepo is already constructed for the
	// reconciler in 221; we pass the same value as LookupRepo.
	torrentsyncQuery := torrentsync.NewQuery(
		torrentsyncStore, qbitTorrentsRepo, torrentSeriesMapRepo,
	)
	seriesTorrentsHandler := handlers.NewSeriesTorrentsHandler(
		torrentsyncQuery, seriesCacheRepo, sdSeriesRepo, log,
	)

	httpServer := httpserver.NewServer(cfg.HTTP, scanUC, webhookUC,
		checker, scanRepo, decisionRepo, grabRepo,
		adminRepo, loginLimiter, webhookLimiter,
		instanceReg,
		cooldownRepo, grabUC, rescanUC,
		instanceCRUDHandler, instanceProbeHandler, runtimeConfigHandler,
		qbitSettingsHandler, externalServicesHandler, oidcUC,
		webhookReconciler, webhookStatusCache,
		seriesCacheRepo, counterRepo, watchdogRollupHandler,
		watchdogBlacklistHandler, watchdogSeasonsHandler, webhooksAggregateHandler,
		mediaHandler, seriesDetailHandler, seriesSeasonHandler, seriesCastHandler,
		peopleHandler, seriesRefreshHandler,
		seriesTorrentsHandler, timezoneHandler, log)

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

	// Phase 11 — background webhook reconcile safety net (041d).
	// The closure over holder.load is reload-aware: every publish
	// swaps the underlying map, so newly-added Sonarr instances
	// appear in the next tick without their own subscriber.
	webhookReconcileLoopVal := newWebhookReconcileLoop(
		webhookReconciler,
		webhookStatusCache,
		holder.load,
		log,
	)
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		webhookReconcileLoopVal.Run(rootCtx)
	}()

	// Build the boot scheduler (if cron is enabled) so the
	// subscriber starts in the same state as the snapshot. Note:
	// Start is deferred so Story 211 can Register the nightly
	// enrichment job after extSub.Start primes TMDB settings.
	// Story 301: closure factory captures the resolver's current
	// location at construction time. Built fresh on each scheduler
	// rebuild (boot + reload) so a pod restart picks up the
	// PATCH'd value. Already-running jobs do NOT pick up live
	// PATCHes — see story known_limitations.
	schedulerFactory := func(schedule string, jitter time.Duration, logger *slog.Logger) *scheduler.Scheduler {
		return scheduler.NewWithLocation(schedule, jitter, logger, tzResolver.Get())
	}
	var bootScheduler *scheduler.Scheduler
	if cfg.Cron.Enabled {
		bootScheduler = schedulerFactory(cfg.Cron.Schedule, cfg.Cron.Jitter, log)
	}

	// Pull the AuthRuntime pointer out of the http server's auth
	// handler so we can hand it to the reload subscriber.
	authHandler := httpServer.AuthHandler()
	var authRuntimePtr *middleware.AuthRuntimePointer
	if authHandler != nil {
		authRuntimePtr = authHandler.AuthRuntime()
	}

	subSched, subClients, err := startSubscribers(rootCtx, &bgWG, bus, log,
		bootScheduler, reload.SchedulerFactory(schedulerFactory),
		scanUC, sonarrClientsByName,
		clientFactory, checker, wd, holder, sweeper,
		regrabLoopVal, torrentsyncLoopVal, qbitLoader,
		&globalLimiterPtr, snap.GlobalRateLimit, authRuntimePtr, httpServer.Engine(),
		runtimeRepo, bootCfg.Auth.OIDCClientSecret)
	if err != nil {
		return nil, fmt.Errorf("start subscribers: %w", err)
	}

	oidcProviderSub := reload.NewOIDCProviderSubscriber(oidcCache, log)
	go oidcProviderSub.Run(rootCtx, bus, func() {})

	// Story 202 (S-2) — external services subscriber. Primes its cache
	// eagerly so the first Phase C/D client.Get() works before any bus
	// publish. No new barrier channel is added to startSubscribers
	// because downstream consumers (Phase C/D) don't exist yet; the
	// boot publish below still flows through the subscriber's bus
	// channel and triggers a second apply.
	extSub.Start(rootCtx, nil)

	// Story 211 (C-2) repositories — used by the enrichment dispatcher
	// + nightly stale scan. seriesRepo already exists above.
	seriesTextsRepo := repositories.NewSeriesTextsRepository(db)
	seasonsRepo := repositories.NewSeasonsRepository(db)
	episodesRepo := repositories.NewEpisodesRepository(db)
	episodeTextsRepo := repositories.NewEpisodeTextsRepository(db)
	peopleRepo := repositories.NewPeopleRepository(db)
	seriesPeopleRepo := repositories.NewSeriesPeopleRepository(db)
	genresRepo := repositories.NewGenresRepository(db)
	genresI18nRepo := repositories.NewGenresI18nRepository(db)
	keywordsRepo := repositories.NewKeywordsRepository(db)
	keywordsI18nRepo := repositories.NewKeywordsI18nRepository(db)
	networksRepo := repositories.NewNetworksRepository(db)
	companiesRepo := repositories.NewCompaniesRepository(db)
	videosRepo := repositories.NewVideosRepository(db)
	contentRatingsRepo := repositories.NewContentRatingsRepository(db)
	externalIDsRepo := repositories.NewExternalIDsRepository(db)
	recommendationsRepo := repositories.NewRecommendationsRepository(db)
	syncLogRepo := repositories.NewSyncLogRepository(db)
	// Story 212 (C-3) — person enrichment + cold-start backfill.
	personBiographiesRepo := repositories.NewPersonBiographiesRepository(db)
	personCreditsRepo := repositories.NewPersonCreditsRepository(db)
	coldStartScanner := NewColdStartScannerAdapter(seriesRepo)

	// Story 211 (C-2) — wire enrichment dispatcher. extSub is primed,
	// so TMDB settings are available. wireEnrichment returns a nil
	// dispatcher when TMDB is disabled / unconfigured (boot stays
	// green on a fresh install).
	enrichRepos := enrichmentRepoBundle{
		Series:            seriesRepo,
		SeriesTexts:       seriesTextsRepo,
		Seasons:           seasonsRepo,
		Episodes:          episodesRepo,
		EpisodeTexts:      episodeTextsRepo,
		People:            peopleRepo,
		SeriesPeople:      seriesPeopleRepo,
		Genres:            genresRepoAdapter{main: genresRepo, i18n: genresI18nRepo},
		Keywords:          keywordsRepoAdapter{main: keywordsRepo, i18n: keywordsI18nRepo},
		Networks:          networksRepo,
		Companies:         companiesRepo,
		Videos:            videosRepoAdapter{inner: videosRepo},
		ContentRatings:    contentRatingsRepoAdapter{inner: contentRatingsRepo},
		ExternalIDs:       externalIDsRepoAdapter{inner: externalIDsRepo},
		Recommendations:   recommendationsRepo,
		SyncLog:           syncLogRepo,
		PersonBiographies: personBiographiesRepo,
		PersonCredits:     personCreditsRepoAdapter{inner: personCreditsRepo},
		ColdStartScanner:  coldStartScanner,
		LibraryWithIMDB:   NewOMDbBatchScannerAdapter(seriesRepo),
		MediaAssets:       mediaAssetsRepo,
		MediaStore:        mediaStoreImpl,
	}
	enrichBundle, err := wireEnrichment(rootCtx, extSub, enrichRepos, txr, quotaCounter, log)
	if err != nil {
		return nil, fmt.Errorf("wire enrichment: %w", err)
	}

	// Story 217 (H-2) — late-bind the dispatcher into the people use
	// case's enqueuer holder. enrichBundle.Dispatcher is nil when
	// enrichment is disabled (cold boot / dev mode); the holder
	// no-ops on nil so the use case continues to return 200 +
	// degraded for stub persons.
	if enrichBundle != nil && enrichBundle.Dispatcher != nil {
		peopleEnqueuerHolder.set(enrichBundle.Dispatcher)
	}

	// Register the nightly stale scan into the boot scheduler if cron
	// is enabled. Done BEFORE Start (now StartRegistered via the
	// legacy wrapper) so the registry is build-once.
	if bootScheduler != nil && enrichBundle != nil && enrichBundle.Nightly != nil {
		if err := bootScheduler.Register("enrichment-nightly", "0 4 * * *",
			enrichBundle.Nightly); err != nil {
			return nil, fmt.Errorf("register nightly enrichment: %w", err)
		}
	}

	// Story 213 (D-1) — OMDb daily batch + budget reset.
	// 04:00 — reset the in-process budget counter (must precede the
	// 04:30 batch so the batch runs against a fresh budget).
	// 04:30 — fan out library series with stale OMDb sync into the
	// enrichment dispatcher at PriorityCold.
	if bootScheduler != nil && enrichBundle != nil {
		// 305: in the DB-backed path the budget guard rotates at UTC
		// midnight implicitly — no explicit Reset needed. Only the
		// in-process fallback (no QuotaCounter) keeps the daily reset
		// cron, because its atomic counter must be Store(initial) at
		// midnight to refill.
		if !enrichBundle.UsesQuotaCounter && enrichBundle.OMDbBudgetReset != nil {
			if err := bootScheduler.Register("omdb-budget-reset", "0 4 * * *",
				enrichBundle.OMDbBudgetReset); err != nil {
				return nil, fmt.Errorf("register omdb budget reset: %w", err)
			}
		}
		if enrichBundle.OMDbDailyBatch != nil {
			if err := bootScheduler.Register("omdb-daily-batch", "30 4 * * *",
				enrichBundle.OMDbDailyBatch); err != nil {
				return nil, fmt.Errorf("register omdb daily batch: %w", err)
			}
		}
		// 305: daily GC sweep for the external_service_quota_state
		// table. Deletes windows older than 7 days so the table stays
		// bounded at #services × 7 rows at steady state. Runs at
		// 04:15 — between budget-reset (which is skipped in DB-mode)
		// and omdb-daily-batch (which runs at 04:30).
		if err := bootScheduler.Register("quota-counter-gc", "15 4 * * *",
			func(ctx context.Context) {
				cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
				deleted, err := quotaCounter.Reset(ctx, cutoff)
				if err != nil {
					log.WarnContext(ctx, "quota.counter.gc.failed",
						slog.String("error", err.Error()))
					return
				}
				log.InfoContext(ctx, "quota.counter.gc.swept",
					slog.Time("cutoff", cutoff),
					slog.Int64("deleted_rows", deleted))
			}); err != nil {
			return nil, fmt.Errorf("register quota-counter-gc: %w", err)
		}
	}

	// Story 218 (E-2) — weekly GC at Sunday 05:00. Best-effort
	// sub-tasks: orphan canon series sweep (90d grace) → media
	// asset sweep (30d cooldown vs live-hash set) → qbit event
	// prune (skipped until A-3 lands the table). Registered
	// unconditionally — the scheduler decides whether to fire it
	// based on cfg.Cron.Enabled.
	if bootScheduler != nil {
		liveAssetsRepo := repositories.NewLiveAssetsRepository(db)
		weeklyJob := gc.WeeklyJob{
			OrphanSeries: gc.OrphanSeriesDeps{
				Repo:   seriesRepo,
				Logger: log,
			}.Build(),
			MediaSweep: gc.MediaSweepDeps{
				LiveSet: liveAssetsRepo,
				Assets:  mediaAssetsRepo,
				Store:   mediaStoreImpl,
				Logger:  log,
			}.Build(),
			EventPrune: gc.EventPruneDeps{
				DB:     db,
				Logger: log,
			}.Build(),
			Logger: log,
		}
		if err := bootScheduler.Register("weekly-gc", "0 5 * * 0", weeklyJob.Run); err != nil {
			return nil, fmt.Errorf("register weekly-gc: %w", err)
		}
	}

	// Start the boot scheduler now that all jobs are registered. The
	// legacy Start(ctx, scanUC) wrapper internally Register(ScanJobName)
	// + StartRegistered.
	if bootScheduler != nil {
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

	// Story 212 — cold-start backfill. Background goroutine: scan
	// series rows missing sync_log(tmdb_series) and enqueue at
	// PriorityCold. Runs AFTER dispatcher.Start (inside wireEnrichment)
	// + bootScheduler.Start so every consumer is alive. bgWG.Add(1)
	// ensures the scan drains on shutdown rather than racing rootCancel.
	// Idempotent re-runs are harmless.
	if enrichBundle != nil && enrichBundle.ColdStart != nil {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			enrichBundle.ColdStart(rootCtx)
		}()
	}

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

	// Story 211 — stop enrichment dispatcher BEFORE the scheduler so
	// the in-flight series worker drains before the cron tears down.
	if enrichBundle != nil && enrichBundle.Dispatcher != nil {
		enrichBundle.Dispatcher.Close()
	}
	// Story 214 (F-1) — drain the media pre-warm pipeline AFTER the
	// dispatcher closes (no more new pre-warm enqueues will land) so
	// the downloader exits cleanly.
	if enrichBundle != nil && enrichBundle.MediaEnqueuer != nil {
		enrichBundle.MediaEnqueuer.Close()
	}
	if enrichBundle != nil && enrichBundle.MediaDownloader != nil {
		enrichBundle.MediaDownloader.Close()
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

// regrabInstanceRegistry adapts handlers.InstanceRegistry to the
// application/regrab.InstanceRegistry interface. The Get(name) →
// (scan.Instance, bool) semantics are a thin nil-safe wrapper.
type regrabInstanceRegistry struct {
	reg handlers.InstanceRegistry
}

func (r regrabInstanceRegistry) Get(name string) (scan.Instance, bool) {
	if r.reg.Load == nil {
		return scan.Instance{}, false
	}
	inst, ok := r.reg.Load()[name]
	return inst, ok
}

// qbitSettingsLoaderFunc is a function-typed shim that satisfies
// qbitSettingsLoader. Defined here so the fanout closure can be
// declared inline above without a named struct.
type qbitSettingsLoaderFunc func(ctx context.Context) map[string]regrab.Settings

func (f qbitSettingsLoaderFunc) Load(ctx context.Context) map[string]regrab.Settings {
	return f(ctx)
}

// personCreditsAdapter projects repositories.PersonCredit rows
// down to the H-1 composer-internal PersonCreditRef shape (Story
// 216). The projection is cheap (two field copies) and keeps the
// application layer free of the repository's wide PersonCredit
// struct.
type personCreditsAdapter struct {
	r *repositories.PersonCreditsRepository
}

func (a personCreditsAdapter) ListByPerson(ctx context.Context, personID int64) ([]seriesdetail.PersonCreditRef, error) {
	rows, err := a.r.ListByPerson(ctx, personID)
	if err != nil {
		return nil, err
	}
	out := make([]seriesdetail.PersonCreditRef, 0, len(rows))
	for _, pc := range rows {
		out = append(out, seriesdetail.PersonCreditRef{
			MediaType:   pc.MediaType,
			TMDBMediaID: pc.TMDBMediaID,
		})
	}
	return out, nil
}

// peopleReaderAdapter projects PeopleRepository onto the H-2
// PeopleReader port — GetByTMDBID for the hot resolution path,
// GetWithBio (renamed from repo's Get) for the bio-resolving
// path. The renaming is local; the production repository's
// method is `Get(ctx, id, language)`.
type peopleReaderAdapter struct {
	r *repositories.PeopleRepository
}

func (a peopleReaderAdapter) GetByTMDBID(ctx context.Context, tmdbID int) (dompeople.Person, error) {
	return a.r.GetByTMDBID(ctx, tmdbID)
}

func (a peopleReaderAdapter) GetWithBio(ctx context.Context, id int64, language string) (dompeople.Person, error) {
	return a.r.Get(ctx, id, language)
}

// personCreditsReaderAdapter projects PersonCreditsRepository
// onto the H-2 PersonCreditsReader port. The repository's
// ListByPerson returns []PersonCreditModel; the adapter converts
// to []dompeople.PersonCredit row by row.
type personCreditsReaderAdapter struct {
	r *repositories.PersonCreditsRepository
}

func (a personCreditsReaderAdapter) ListByPerson(ctx context.Context, personID int64) ([]dompeople.PersonCredit, error) {
	rows, err := a.r.ListByPerson(ctx, personID)
	if err != nil {
		return nil, err
	}
	out := make([]dompeople.PersonCredit, 0, len(rows))
	for _, m := range rows {
		out = append(out, modelToPersonCredit(m))
	}
	return out, nil
}

// modelToPersonCredit maps PersonCreditModel → domain
// PersonCredit. Year passes through as the synthetic date
// (year, 1, 1) so downstream code that reads Year from
// ReleaseDate works; PosterPath is mapped to PosterAsset (the
// v1 H-2 layer treats both as pass-through strings, formal asset
// migration deferred).
func modelToPersonCredit(m database.PersonCreditModel) dompeople.PersonCredit {
	var rel *time.Time
	if m.Year != nil {
		t := time.Date(*m.Year, 1, 1, 0, 0, 0, 0, time.UTC)
		rel = &t
	}
	return dompeople.PersonCredit{
		ID:            m.ID,
		PersonID:      m.PersonID,
		MediaType:     m.MediaType,
		TMDBMediaID:   int64(m.TMDBMediaID),
		TMDBCreditID:  m.TMDBCreditID,
		Kind:          dompeople.SeriesCreditKind(m.Kind),
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		CharacterName: m.CharacterName,
		Department:    m.Department,
		Job:           m.Job,
		EpisodeCount:  m.EpisodeCount,
		ReleaseDate:   rel,
		PosterAsset:   m.PosterPath,
		TMDBRating:    m.VoteAverage,
		TMDBVotes:     m.TMDBVotes,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

// personEnqueuerHolder late-binds the enrichment dispatcher into
// the H-2 people use case. The dispatcher is constructed inside
// wireEnrichment, but the people use case is built earlier (so it
// can be passed to httpserver.NewServer). The holder is wired
// after enrichBundle is assembled; until then Enqueue no-ops
// (nil-OK by contract — the use case still returns 200 + degraded
// for stub persons on cold boot / disabled enrichment).
type personEnqueuerHolder struct {
	inner appenrich.Dispatcher
}

func (h *personEnqueuerHolder) set(d appenrich.Dispatcher) { h.inner = d }

func (h *personEnqueuerHolder) Enqueue(kind appenrich.EntityKind, id int64, p appenrich.Priority) {
	if h.inner == nil {
		return
	}
	h.inner.Enqueue(kind, id, p)
}

// Close satisfies appenrich.Dispatcher so the same holder serves
// both PersonEnqueuer (Enqueue-only) and seriesrefresh.Deps.Dispatcher
// (Enqueue + Close). The dispatcher's actual Close runs via
// enrichBundle.Dispatcher.Close() at shutdown, so this holder no-ops.
func (h *personEnqueuerHolder) Close() {}

// seriesRefreshSeriesAdapter projects SeriesRepository.Get onto the
// thin seriesrefresh.CanonView shape so the use case stays free of
// the domain/series import. Story 218 (E-2).
type seriesRefreshSeriesAdapter struct {
	r *repositories.SeriesRepository
}

func (a seriesRefreshSeriesAdapter) Get(ctx context.Context, id int64) (seriesrefresh.CanonView, error) {
	c, err := a.r.Get(ctx, id)
	if err != nil {
		return seriesrefresh.CanonView{}, err
	}
	return seriesrefresh.CanonView{ID: c.ID, IMDBID: c.IMDBID}, nil
}

// seriesRefreshCastAdapter implements seriesrefresh.TopCastReader by
// calling SeriesPeopleRepository.ListBySeries (the composer's existing
// path) and slicing the first N person ids. Story 218 (E-2).
type seriesRefreshCastAdapter struct {
	r *repositories.SeriesPeopleRepository
}

func (a seriesRefreshCastAdapter) TopCastPersonIDs(ctx context.Context, seriesID int64, limit int) ([]int64, error) {
	credits, err := a.r.ListBySeries(ctx, seriesID, dompeople.SeriesCreditCast)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > len(credits) {
		limit = len(credits)
	}
	out := make([]int64, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, credits[i].PersonID)
	}
	return out, nil
}
