package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	authapp "github.com/alexmorbo/seasonfill/application/auth"
	"github.com/alexmorbo/seasonfill/application/evaluate"
	appextsvc "github.com/alexmorbo/seasonfill/application/externalservices"
	"github.com/alexmorbo/seasonfill/application/gc"
	"github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/instance"
	apppeople "github.com/alexmorbo/seasonfill/application/people"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/application/seriesrefresh"
	"github.com/alexmorbo/seasonfill/application/torrentsync"
	webhookuc "github.com/alexmorbo/seasonfill/application/webhook"
	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/cmd/server/wiring"
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
)

// Options carries optional construction-time hooks. OnReady mirrors the
// existing runWithContext onReady callback used by E2E tests to detect
// "bus is wired". It fires from Run, AFTER the boot snapshot has been
// published and BEFORE the HTTP serve goroutine starts — same temporal
// position as the original runWithContext implementation.
type Options struct {
	OnReady func(*runtime.Bus)
}

// Server is the seasonfill composition root + lifecycle driver.
// Fields are the subset of locals from the original runWithContext that
// the HTTP-serve loop and shutdown ladder need to access.
type Server struct {
	log        *slog.Logger
	cfg        wiring.HTTPServeConfig
	bus        *runtime.Bus
	bgWG       *sync.WaitGroup
	lifecycle  *lifecycleGroup
	rootCancel context.CancelFunc

	httpServer   *httpserver.Server
	scanUC       *scan.UseCase
	scanRepo     *repositories.ScanRepository
	enrichBundle *EnrichmentBundle
	subSched     *reload.SchedulerSubscriber
	persistence  *wiring.PersistenceBundle
	onReady      func(*runtime.Bus)
}

// New wires the server. The body below is the verbatim lift-and-shift of
// runWithContext from `bootCfg, err := config.FromEnv()` through
// `notifyTestContext(...)`. The HTTP serve goroutine + signal-select +
// shutdown ladder move to Run/Shutdown. Every `// Story XXX` comment is
// preserved as institutional knowledge per B-11 design doc §1.3.
//
// On error returns after `bus` and `rootCancel` are created the original
// code relied on `defer bus.Close()` + `defer rootCancel()` at function
// return. We preserve that semantic with an `armed` sentinel: defers fire
// on early error returns, but are disarmed on the successful return so
// the bus and rootCancel survive into Shutdown.
func New(ctx context.Context, opts Options) (*Server, error) {
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

	persistence, err := wiring.BuildPersistence(ctx, bootCfg, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). Subsequent B-11 stories
	// will progressively retire these as wirers absorb the dependent
	// blocks. After story 330, runtimeRepo + instanceRepo aliases stay
	// because they are still consumed by instance.New, qbitSettingsUC,
	// watchdogInstanceAdapter, qbitLoader, and startSubscribers below.
	// appSettingsRepo is intentionally NOT rebound: it has no direct
	// reference in the surviving body — story 330+ consumers reach it
	// via persistence.AppSettingsRepo.
	db := persistence.DB
	cipher := persistence.Cipher
	runtimeRepo := persistence.RuntimeRepo
	instanceRepo := persistence.InstanceRepo
	quotaCounter := persistence.QuotaCounter
	tzResolver := persistence.TZResolver
	timezoneHandler := persistence.TimezoneHandler

	bgCtx := context.Background()

	// Bus is constructed BEFORE BuildRuntimeConfig (story 330 reorder)
	// so the wirer can take *runtime.Bus as input and own the runtime
	// config UC construction. The `armed` sentinel mirrors the pre-330
	// pattern: defer fires bus.Close on every early error return; the
	// success path at the bottom of New() disarms before returning.
	bus := runtime.NewBus(log)
	armed := true
	defer func() {
		if armed {
			bus.Close()
		}
	}()

	runtimecfg, err := wiring.BuildRuntimeConfig(ctx, persistence, bootCfg, bus, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-330 names verbatim so every downstream call site
	// (httpserver.NewServer, startSubscribers, scheduler factory,
	// webhookReconciler, etc.) keeps working unchanged.
	snap := runtimecfg.Snap
	runtimeConfigHandler := runtimecfg.Handler
	cfg := runtimecfg.ServeConfig

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

	clientFactory := reload.NewSonarrClientFactory(&globalLimiterPtr, log)
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
	holder := adapters.NewInstanceMapHolder(scanInstancesByName)

	// Registry is constructed ONCE here. checker.Registry() returns a
	// stable pointer for the life of the process; the reload subscriber
	// mutates membership via ReplaceClients, NOT by replacing the pointer.
	checker := healthcheck.New(db, sonarrClients)

	rootCtx, rootCancel := context.WithCancel(ctx)
	// `defer rootCancel()` in the original fired on every return.
	// Mirror the bus pattern: error returns cancel, success path
	// transfers ownership to the Server.
	defer func() {
		if armed {
			rootCancel()
		}
	}()

	// M-9: track every background goroutine so we can wait for them to exit
	// before closing the DB handle below.
	//
	// bgWG is retained for cross-package wiring that still expects
	// *sync.WaitGroup (scan.UseCase.WithWaitGroup, newRegrabLoop,
	// newTorrentsyncLoop, startSubscribers, SonarrClientsSubscriber).
	// lifecycle owns the inline goroutines spawned directly from
	// Server.New (B-11 step 3 / story 325). Both are drained in
	// Shutdown — see the ladder at the end of this file.
	var bgWG sync.WaitGroup
	lifecycle := newLifecycleGroup(log)

	lifecycle.Go(rootCtx, "healthcheck", func(ctx context.Context) {
		checker.Run(ctx, 30*time.Second)
	})

	// Watchdog rechecks Unavailable* instances at per-state cadences (D-2.3).
	wd := watchdog.New(checker.Registry(), checker, log, cfgByName)
	lifecycle.Go(rootCtx, "watchdog", func(ctx context.Context) {
		wd.Run(ctx)
	})

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
	rescanUC := rescan.NewUseCase(decisionRepo, grabRepo, scanRepo, scanUC, evaluator, holder.Load, log)

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
			h := holder.Load()
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
			inst, ok := holder.Load()[name]
			if !ok {
				return 0
			}
			return inst.Config.Cooldown.GUIDAfterFailedImport
		},
		Logger: log,
		SonarrClientFor: func(name string) (ports.SonarrClient, bool) {
			if h := holder.Load(); h != nil {
				if inst, ok := h[name]; ok && inst.Client != nil {
					return inst.Client, true
				}
			}
			return nil, false
		},
		InstanceFor: func(name string) (runtime.InstanceSnapshot, bool) {
			if h := holder.Load(); h != nil {
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
	instanceReg := handlers.InstanceRegistry{Load: holder.Load}

	webhookStatusCache := webhookinstall.NewStatusCache()
	webhookReconciler := webhookinstall.New(webhookinstall.Deps{
		Lookup:    adapters.NewWebhookReconcileLookup(instanceReg),
		PublicURL: webhookinstall.PublicURLFromContext,
		Cache:     webhookStatusCache,
		APIKey:    cfg.HTTP.Auth.APIKey,
		Logger:    log,
	})

	instanceUC := instance.New(instanceRepo, runtimeRepo, cipher, bus, log).
		WithWebhookReconciler(adapters.ReconcilerAdapter{Inner: webhookReconciler}).
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
		WithWebhookChecker(adapters.NewWebhookChecker(instanceReg))
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
		adapters.NewRegrabInstanceRegistry(instanceReg),
		infraregrab.QbitClientFactoryFunc{},
		infraregrab.DetectorFactoryFunc{},
		grabRepo, cooldownRepo, blacklistRepo, noBetterCounterRepo,
		evaluator, grabUC,
		log,
	).WithMetrics(observability.WatchdogMetricsAdapter{}).
		WithDecisions(decisionRepo)

	// regrab loop owns the per-instance polling goroutines; SwapSettings
	// is called from the OnApplied fanout below.
	regrabLoopVal := loops.NewRegrabLoop(regrabUC, observability.WatchdogMetricsAdapter{}, &bgWG, log)
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
	torrentsyncFactory := loops.NewTorrentsyncSessionFactoryAdapter(
		infraregrab.QbitClientFactoryFunc{},
		qbitSettingsUC,
	)
	// 221 (A-3) — reconciler (torrentSeriesMapRepo already
	// constructed above for the webhook same-tx write).
	// sonarrFor wires the per-instance Sonarr client lookup the
	// reconciler needs for sources 3 + 4. Production wiring reuses
	// the instance holder; the concrete *sonarr.Client satisfies
	// torrentsync.SonarrReconciler (its QueueAll + GrabHistoryPaged
	// are exactly the two methods in the port).
	sonarrFor := func(instance string) (torrentsync.SonarrReconciler, bool) {
		h := holder.Load()
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
	torrentsyncLoopVal := loops.NewTorrentsyncLoop(
		loops.NewProductionTorrentsyncRunner(torrentsyncUC, log),
		&bgWG, log,
	)
	torrentsyncLoopVal.Start(rootCtx)

	// 047a — watchdog rollup handler wiring.
	watchdogInstanceAdapter := adapters.NewWatchdogInstanceLister(instanceRepo, cipher)
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
	qbitLoader := adapters.QbitSettingsLoaderFunc(func(ctx context.Context) map[string]regrab.Settings {
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
	extSub := adapters.NewExternalServicesSubscriber(bus, log)
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
	// Story 321: the handler is constructed BEFORE wireEnrichment (and
	// thus before the on-demand fetcher exists). The fetcher is plumbed
	// in via a setter after enrichBundle returns. Until then the handler
	// serves the embedded SVG placeholder on pending hashes — visually
	// stable while the media pipeline boots.
	mediaHandler := handlers.NewMediaHandler(handlers.MediaHandlerDeps{
		Store:           mediaStoreImpl,
		Repo:            mediaAssetsRepo,
		PendingResolver: mediaAssetsRepo, // story 320: satisfies GetSourceURLByHash
		Logger:          log,
		// OnDemandFetcher is late-bound below (see story 321 wiring after enrichBundle).
	})

	// Story 312 + Story 320: media resolver for the seriesdetail composer.
	// nil-OK `mediaAssetsRepo` falls back to a nop resolver inside
	// NewMediaResolver → every wire field stays nil and the frontend
	// renders monograms. *MediaAssetsRepository satisfies the widened
	// MediaHashLookupPort (HashForSourceURL + EnsurePending) by virtue
	// of the new EnsurePending method (story 320).
	var mediaHashLookup seriesdetail.MediaHashLookupPort
	if mediaAssetsRepo != nil {
		mediaHashLookup = mediaAssetsRepo
	}
	// Story 316: enqueuer + fetcher are late-bound via SetSideEffects
	// after wireEnrichment returns — the media pipeline doesn't exist
	// yet at this point in boot.
	seriesDetailMediaResolver := seriesdetail.NewMediaResolver(mediaHashLookup, nil, nil, log)

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
			h := holder.Load()
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
		PersonCredits:     adapters.NewPersonCreditsAdapter(sdPersonCreditsRepo),
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
	peopleEnqueuerHolder := adapters.NewPersonEnqueuerHolder()
	peopleUC := apppeople.NewUseCase(apppeople.Deps{
		People:        adapters.NewPeopleReaderAdapter(sdPeopleRepo),
		PersonCredits: adapters.NewPersonCreditsReaderAdapter(sdPersonCreditsRepo),
		SeriesByTMDB:  sdSeriesRepo,
		SeriesCache:   seriesCacheRepo,
		SyncLog:       sdSyncLogRepo,
		Enqueuer:      peopleEnqueuerHolder,
		MediaResolver: seriesDetailMediaResolver,
		Logger:        log,
	})
	peopleHandler := handlers.NewPeopleHandler(peopleUC, log)

	// Story 218 (E-2) — series refresh trigger. Reuses the
	// peopleEnqueuerHolder so the same late-binding dispatcher
	// satisfies both the H-2 use case AND the refresh path.
	seriesRefreshUC, err := seriesrefresh.New(seriesrefresh.Deps{
		SeriesCache:  seriesCacheRepo,
		Series:       adapters.NewSeriesRefreshSeriesAdapter(seriesRepo),
		SeriesPeople: adapters.NewSeriesRefreshCastAdapter(sdSeriesPeopleRepo),
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
	sweeper := loops.NewSweepLoop(cooldownRepo, sweepInterval, log)
	lifecycle.Go(rootCtx, "cooldown-sweeper", func(ctx context.Context) {
		sweeper.Run(ctx)
	})

	// Phase 11 — background webhook reconcile safety net (041d).
	// The closure over holder.load is reload-aware: every publish
	// swaps the underlying map, so newly-added Sonarr instances
	// appear in the next tick without their own subscriber.
	webhookReconcileLoopVal := loops.NewWebhookReconcileLoop(
		webhookReconciler,
		webhookStatusCache,
		holder.Load,
		log,
	)
	lifecycle.Go(rootCtx, "webhook-reconcile", func(ctx context.Context) {
		webhookReconcileLoopVal.Run(ctx)
	})

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
	enrichBundle, err := wireEnrichment(rootCtx, extSub, bootCfg, enrichRepos, txr, quotaCounter, log)
	if err != nil {
		return nil, fmt.Errorf("wire enrichment: %w", err)
	}

	// Story 217 (H-2) — late-bind the dispatcher into the people use
	// case's enqueuer holder. enrichBundle.Dispatcher is nil when
	// enrichment is disabled (cold boot / dev mode); the holder
	// no-ops on nil so the use case continues to return 200 +
	// degraded for stub persons.
	if enrichBundle != nil && enrichBundle.Dispatcher != nil {
		peopleEnqueuerHolder.Set(enrichBundle.Dispatcher)
	}

	// Story 316 — late-bind the enqueuer + on-demand fetcher onto the
	// seriesdetail.MediaResolver. Both are nil when the media subsystem
	// is unwired (no MediaStore + MediaAssets repo); the resolver then
	// stays in the pre-316 behaviour (sync resolves to nil + no async
	// enqueue). *appmedia.Enqueuer already satisfies the
	// seriesdetail.MediaEnqueuer interface shape.
	if enrichBundle != nil && seriesDetailMediaResolver != nil {
		var mediaEnq seriesdetail.MediaEnqueuer
		if enrichBundle.MediaEnqueuer != nil {
			mediaEnq = enrichBundle.MediaEnqueuer
		}
		seriesDetailMediaResolver.SetSideEffects(mediaEnq, enrichBundle.MediaOnDemand)
	}

	// Story 321 — late-bind the on-demand fetcher onto the MediaHandler
	// so GET /api/v1/media/:hash can synchronously fill pending rows on
	// a cache miss. enrichBundle.MediaOnDemand is nil when the media
	// subsystem is unwired; the handler stays on the embedded SVG
	// placeholder path in that case.
	if enrichBundle != nil && mediaHandler != nil && enrichBundle.MediaOnDemand != nil {
		mediaHandler.SetOnDemandFetcher(enrichBundle.MediaOnDemand)
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

	// Story 212 + 318 — cold-start backfill loop. Background
	// goroutine: scan series rows missing sync_log(tmdb_series)
	// and enqueue at PriorityCold, then re-sweep every
	// Enrichment.ColdStartResweepInterval (default 60s) for the
	// lifetime of the process. Picks up rows the dispatcher had
	// to drop on a saturated cold channel during the previous
	// sweep. Runs AFTER dispatcher.Start (inside wireEnrichment)
	// + bootScheduler.Start so every consumer is alive.
	// bgWG.Add(1) keeps shutdown waiting for the goroutine to
	// exit on rootCtx cancellation.
	if enrichBundle != nil && enrichBundle.ColdStart != nil {
		lifecycle.Go(rootCtx, "cold-start-backfill", func(ctx context.Context) {
			enrichBundle.ColdStart(ctx)
		})
	}

	// Re-publish the boot snapshot now that subscribers are alive
	// — they all apply it once and increment their success metric.
	bus.Publish(rootCtx, snap)

	// notifyTestContext fires testContextHook (integration builds only) so
	// E2E tests can assert per-subscriber state. The call is a no-op in
	// production builds (testcontext_stub.go provides the empty function).
	notifyTestContext(bus, subSched, subClients, authRuntimePtr, &globalLimiterPtr, holder.Load, checker.Snapshot)

	// Construction succeeded — disarm the bus.Close + rootCancel defers
	// so the Server owns these resources until Shutdown.
	armed = false
	return &Server{
		log:          log,
		cfg:          cfg,
		bus:          bus,
		bgWG:         &bgWG,
		lifecycle:    lifecycle,
		rootCancel:   rootCancel,
		httpServer:   httpServer,
		scanUC:       scanUC,
		scanRepo:     scanRepo,
		enrichBundle: enrichBundle,
		subSched:     subSched,
		persistence:  persistence,
		onReady:      opts.OnReady,
	}, nil
}

// Run fires the OnReady hook (same temporal position as the original
// runWithContext) then starts the HTTP server and blocks until ctx is
// cancelled or the HTTP server returns. Always calls Shutdown before
// returning.
func (s *Server) Run(ctx context.Context) error {
	if s.onReady != nil {
		s.onReady(s.bus)
	}
	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- s.httpServer.Start() }()

	select {
	case <-ctx.Done():
		s.log.Info("shutdown signal received")
	case err := <-serverErrCh:
		if err != nil {
			s.log.Error("http server stopped", slog.String("error", err.Error()))
		}
	}
	return s.Shutdown(ctx)
}

// Shutdown drains HTTP server, enrichment pipeline, scheduler, in-flight
// scans, background goroutines, then closes the DB handle and the reload
// bus — in the same order as the original runWithContext shutdown ladder.
func (s *Server) Shutdown(parentCtx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		s.log.Error("http shutdown error", slog.String("error", err.Error()))
	}

	// Story 211 — stop enrichment dispatcher BEFORE the scheduler so
	// the in-flight series worker drains before the cron tears down.
	if s.enrichBundle != nil && s.enrichBundle.Dispatcher != nil {
		s.enrichBundle.Dispatcher.Close()
	}
	// Story 214 (F-1) — drain the media pre-warm pipeline AFTER the
	// dispatcher closes (no more new pre-warm enqueues will land) so
	// the downloader exits cleanly.
	if s.enrichBundle != nil && s.enrichBundle.MediaEnqueuer != nil {
		s.enrichBundle.MediaEnqueuer.Close()
	}
	if s.enrichBundle != nil && s.enrichBundle.MediaDownloader != nil {
		s.enrichBundle.MediaDownloader.Close()
	}

	if cur := s.subSched.Current(); cur != nil {
		stopCtx := cur.Stop()
		select {
		case <-stopCtx.Done():
		case <-time.After(5 * time.Second):
		}
	}

	grace := s.cfg.Scan.ShutdownGrace
	if grace <= 0 {
		grace = 60 * time.Second
	}
	waitForScans(parentCtx, s.scanUC, s.scanRepo, s.log, grace)
	s.rootCancel()

	// M-9: drain background goroutines before closing the DB handle.
	// lifecycle covers the 5 inline goroutines spawned by Server.New
	// (healthcheck, watchdog, cooldown-sweeper, webhook-reconcile,
	// cold-start-backfill). bgWG covers cross-package wiring
	// (scan.UseCase, regrabLoop, torrentsyncLoop, startSubscribers,
	// SonarrClientsSubscriber) — migrating those is a follow-up.
	if err := s.lifecycle.Drain(10 * time.Second); err != nil {
		s.log.Warn("lifecycle drain timed out", slog.String("error", err.Error()))
	}
	drainBackground(s.bgWG, 10*time.Second, s.log)

	if sqlDB, err := s.persistence.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
	// Close the reload bus last — the original runWithContext relied on
	// `defer bus.Close()` to fire after the shutdown ladder; we preserve
	// that ordering explicitly here.
	s.bus.Close()
	s.log.Info("seasonfill stopped cleanly")
	return nil
}
