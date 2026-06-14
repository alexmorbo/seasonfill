package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/application/gc"
	apppeople "github.com/alexmorbo/seasonfill/application/people"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/application/seriesrefresh"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/cmd/server/wiring"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	httpserver "github.com/alexmorbo/seasonfill/interface/http"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/logger"
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
	watchdog     *wiring.WatchdogBundle
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
	// blocks. After story 337, instanceRepo no longer needs rebinding —
	// the qbitSettingsUC + watchdogInstanceAdapter + qbitLoader call sites
	// all moved into wiring.BuildRegrab, which reads the repo from the
	// persistence bundle directly. runtimeRepo stays because
	// startSubscribers still consumes it for the OIDC subscriber.
	// appSettingsRepo is intentionally NOT rebound: it has no direct
	// reference in the surviving body — story 330+ consumers reach it
	// via persistence.AppSettingsRepo. cipher was retired with story
	// 339: the last consumer (infraextsvc.NewRepository) moved into
	// wiring.BuildExtSvc, which reads it from the persistence bundle.
	db := persistence.DB
	runtimeRepo := persistence.RuntimeRepo
	quotaCounter := persistence.QuotaCounter
	tzResolver := persistence.TZResolver
	timezoneHandler := persistence.TimezoneHandler

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

	auth, err := wiring.BuildAuth(ctx, persistence, bootCfg, bus, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-331 names verbatim so every downstream call site
	// (httpserver.NewServer, OIDCProviderSubscriber) keeps working
	// unchanged.
	adminRepo := auth.AdminRepo
	oidcCache := auth.OIDCCache
	oidcUC := auth.OIDCUC
	loginLimiter := auth.LoginLimiter
	webhookLimiter := auth.WebhookLimiter

	sonarrBundle, err := wiring.BuildSonarr(snap, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-332 names verbatim so every downstream call
	// site (healthcheck.New, watchdog.New, scan.NewUseCase, the
	// reload-aware lookup closures, startSubscribers,
	// notifyTestContext) keeps working unchanged. holder is exposed
	// as a *InstanceMapHolder so its pointer identity stays stable
	// across reload — the OnApplied fanout swaps the inner map via
	// Replace, never the wrapper. globalLimiterPtr is exposed as a
	// pointer to the heap-allocated atomic so every consumer (the
	// ClientFactory closure, GlobalRateLimiterSubscriber, and the
	// testcontext hook) shares the same cell.
	clientFactory := sonarrBundle.ClientFactory
	sonarrClientsByName := sonarrBundle.ClientsByName
	holder := sonarrBundle.Holder
	instanceReg := sonarrBundle.InstanceReg
	globalLimiterPtr := sonarrBundle.GlobalLimiterPtr

	watchdogBundle, err := wiring.BuildWatchdog(persistence, sonarrBundle, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-333 names verbatim so every downstream call site
	// (scan.UseCase.WithHealthRegistry, httpserver.NewServer,
	// startSubscribers, notifyTestContext) keeps working unchanged.
	// The lifecycle.Go spawns below address checker.Run / wd.Run via
	// these aliases — the bundle itself is stored on the Server so
	// future Shutdown wiring can reach the same handles via
	// s.watchdog.
	checker := watchdogBundle.Checker
	wd := watchdogBundle.Watchdog

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
	lifecycle.Go(rootCtx, "watchdog", func(ctx context.Context) {
		wd.Run(ctx)
	})

	scanBundle, err := wiring.BuildScan(persistence, sonarrBundle, watchdogBundle, cfg, &bgWG, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-334 names verbatim so every downstream call site
	// (httpserver.NewServer, webhook UC Deps, scheduler.Start, startup
	// scan goroutine, the regrab use case, the reload subscribers,
	// waitForScans) keeps working unchanged. seriesRepo / seriesCacheRepo
	// / counterRepo are constructed here because BuildScan uses
	// seriesRepo / seriesCacheRepo internally but does not re-expose
	// them on the bundle (they are stateless GORM wrappers; downstream
	// call sites each get their own instance). counterRepo is unrelated
	// to the scan stack but was historically constructed in the same
	// block; it stays here.
	scanRepo := scanBundle.ScanRepo
	decisionRepo := scanBundle.DecisionRepo
	grabRepo := scanBundle.GrabRepo
	cooldownRepo := scanBundle.CooldownRepo
	txr := scanBundle.Txr
	grabUC := scanBundle.GrabUC
	scanUC := scanBundle.ScanUC
	rescanUC := scanBundle.RescanUC
	sweeper := scanBundle.Sweeper

	seriesRepo := repositories.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	counterRepo := repositories.NewCounterRepository(db)

	webhookBundle, err := wiring.BuildWebhook(persistence, sonarrBundle, scanBundle, cfg, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-335 names verbatim so every downstream call site
	// (instance.New().WithWebhookReconciler / WithWebhookStatusCache,
	// httpserver.NewServer, loops.NewWebhookReconcileLoop,
	// webhooksAggregateHandler) keeps working unchanged.
	//
	// TorrentSeriesMapRepo is intentionally NOT rebound — story 338
	// moved torrentsync.NewReconciler + torrentsync.NewQuery into
	// wiring.BuildTorrentsync, which reads the repo from webhookBundle
	// directly. EpisodeStatesRepo is intentionally NOT rebound: no
	// surviving server.go code references it directly (the Syncer
	// captures it inside the bundle).
	webhookUC := webhookBundle.WebhookUC
	webhookReconciler := webhookBundle.Reconciler
	webhookStatusCache := webhookBundle.StatusCache

	instanceBundle, err := wiring.BuildInstance(persistence, webhookBundle, bus, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields preserve
	// the pre-336 names verbatim so every downstream call site
	// (httpserver.NewServer for instanceCRUDHandler / instanceProbeHandler,
	// notifyTestContext) keeps working unchanged. The UC alias is kept for
	// future stories that may need the handle directly from server.go (none
	// in the surviving body — the CRUD handler is the only consumer).
	instanceUC := instanceBundle.UC
	instanceCRUDHandler := instanceBundle.CRUDHandler
	instanceProbeHandler := instanceBundle.ProbeHandler
	_ = instanceUC // reserved — see godoc

	regrabBundle, err := wiring.BuildRegrab(persistence, sonarrBundle, scanBundle, webhookBundle, &bgWG, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-337 names verbatim so every downstream call site
	// (httpserver.NewServer for the four watchdog handlers + qbit settings
	// handler + webhooks aggregate handler, startSubscribers for
	// regrabLoop + qbitLoader) keeps working unchanged. qbitSettingsUC /
	// blacklistRepo / noBetterCounterRepo / regrabUC are intentionally
	// NOT rebound — no surviving server.go body code references them
	// directly (story 338 moved the torrentsyncFactory lookup site into
	// wiring.BuildTorrentsync; the rollup handler captures blacklistRepo
	// inside BuildRegrab; the regrab use case is owned by the RegrabLoop
	// and consumed via SwapSettings through regrabLoopVal).
	qbitSettingsHandler := regrabBundle.QbitSettingsHandler
	regrabLoopVal := regrabBundle.RegrabLoop

	// regrab loop owns the per-instance polling goroutines; SwapSettings
	// is called from the OnApplied fanout below. The constructor moved to
	// BuildRegrab in story 337; only the rootCtx-bearing .Start lives here.
	regrabLoopVal.Start(rootCtx)

	torrentsyncBundle, err := wiring.BuildTorrentsync(
		persistence, sonarrBundle, scanBundle, webhookBundle, regrabBundle, &bgWG, log,
	)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-338 names verbatim so every downstream call site
	// (httpserver.NewServer for seriesTorrentsHandler, startSubscribers
	// for torrentsyncLoopVal) keeps working unchanged. Other bundle
	// fields (Store / Policy / Factory / Reconciler / UC / Query) are
	// not referenced by the surviving body — they live entirely inside
	// BuildTorrentsync now.
	torrentsyncLoopVal := torrentsyncBundle.Loop
	seriesTorrentsHandler := torrentsyncBundle.SeriesTorrentsHandler

	// 220 (A-2) — torrentsync loop's Start needs rootCtx (owned by
	// server.go, not the wirer). Same pattern as regrabLoopVal.Start
	// above (story 337).
	torrentsyncLoopVal.Start(rootCtx)

	// 047a/047b/098a + webhooks aggregate handler — moved to wiring.BuildRegrab
	// (story 337). Rebind for the remainder of New() so the httpserver.NewServer
	// call below keeps the pre-337 names verbatim.
	watchdogRollupHandler := regrabBundle.WatchdogRollupHandler
	watchdogBlacklistHandler := regrabBundle.WatchdogBlacklistHandler
	watchdogSeasonsHandler := regrabBundle.WatchdogSeasonsHandler
	webhooksAggregateHandler := regrabBundle.WebhooksAggregateHandler

	// qBit settings loader for the fanout — moved to wiring.BuildRegrab
	// (story 337). The closure semantics are identical: fresh List + build
	// Settings map on every Load, password decryption centralised via
	// qbitSettingsUC.
	qbitLoader := regrabBundle.QbitLoader

	extSvcBundle, err := wiring.BuildExtSvc(persistence, bootCfg, bus, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-339 names verbatim so every downstream call site
	// (httpserver.NewServer for externalServicesHandler, the
	// extSub.Start(rootCtx, nil) prime below) keeps working unchanged.
	// The extUC alias is intentionally NOT rebound — no surviving
	// server.go code references it directly; it lives inside the bundle.
	extSub := extSvcBundle.Sub
	externalServicesHandler := extSvcBundle.Handler

	mediaBundle, err := wiring.BuildMedia(rootCtx, persistence, bootCfg, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-339 names verbatim so every downstream call site
	// (httpserver.NewServer for mediaHandler, enrichmentRepoBundle for
	// MediaAssets + MediaStore, the seriesdetail MediaResolver fallback,
	// the gc weekly job, the SetOnDemandFetcher late-bind below) keeps
	// working unchanged.
	mediaStoreImpl := mediaBundle.Store
	mediaAssetsRepo := mediaBundle.AssetsRepo
	mediaHandler := mediaBundle.Handler

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
	// effect without a pod restart. The loop itself is constructed
	// by BuildScan (story 334); we only spawn it here on the
	// lifecycle group so Shutdown can drain it.
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
		globalLimiterPtr, snap.GlobalRateLimit, authRuntimePtr, httpServer.Engine(),
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
	notifyTestContext(bus, subSched, subClients, authRuntimePtr, globalLimiterPtr, holder.Load, checker.Snapshot)

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
		watchdog:     watchdogBundle,
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
