package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/cmd/server/wiring"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	httpserver "github.com/alexmorbo/seasonfill/interface/http"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/config"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	infraextsvc "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// Options carries optional construction-time hooks. OnReady fires from
// Run after the boot snapshot is published and before HTTP starts —
// E2E tests use it to detect "bus is wired".
type Options struct {
	OnReady func(*runtime.Bus)
}

// Server is the seasonfill composition root + lifecycle driver.
type Server struct {
	log        *slog.Logger // unwrapped root; handed to wirers that DomainLogger internally
	shutLog    *slog.Logger // pre-domained for shutdown ladder (domain="shutdown")
	cfg        wiring.HTTPServeConfig
	bus        *runtime.Bus
	bgWG       *sync.WaitGroup
	lifecycle  *lifecycleGroup
	rootCancel context.CancelFunc

	httpServer   *httpserver.Server
	scanUC       *scan.UseCase
	scanRepo     *repositories.ScanRepository
	enrichBundle *wiring.EnrichmentBundle
	subSched     *reload.SchedulerSubscriber
	persistence  *wiring.PersistenceBundle
	watchdog     *wiring.WatchdogBundle
	onReady      func(*runtime.Bus)
}

// New wires the server. The `armed` sentinel ensures bus.Close +
// rootCancel defers fire on early error returns but are disarmed on
// success so the resources survive into Shutdown.
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
	// F-4b-8: derive pre-domained loggers for the composition root's own
	// boot/shutdown emissions. The unwrapped `log` is still handed to
	// wirers (BuildPersistence, BuildRuntimeConfig, …) — each of those
	// applies its own DomainLogger wrap internally per F-4b rule #1.
	bootLog := sharedports.DomainLogger(log, "boot")
	shutLog := sharedports.DomainLogger(log, "shutdown")
	bootLog.Info("starting seasonfill (bootstrap config)",
		slog.String("driver", bootCfg.Database.Driver))

	persistence, err := wiring.BuildPersistence(ctx, bootCfg, log)
	if err != nil {
		return nil, err
	}
	// Surviving locals: db feeds the C-2 repo constructors below;
	// quotaCounter is handed to BuildEnrichment. All other persistence
	// fields are consumed by downstream wirers via the bundle.
	db := persistence.DB
	quotaCounter := persistence.QuotaCounter

	// Bus is constructed BEFORE BuildRuntimeConfig so the wirer can
	// take *runtime.Bus as input and own the runtime config UC.
	// F-4b-8: bootLog tags Bus reload-publish records with domain="boot".
	bus := runtime.NewBus(bootLog)
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
	// snap is republished after subscribers start; cfg feeds Build* calls
	// + Run/Shutdown via s.cfg.
	snap := runtimecfg.Snap
	cfg := runtimecfg.ServeConfig

	auth, err := wiring.BuildAuth(ctx, persistence, bootCfg, bus, log)
	if err != nil {
		return nil, err
	}
	// oidcCache feeds the OIDCProviderSubscriber spawn below.
	oidcCache := auth.OIDCCache

	sonarrBundle, err := wiring.BuildSonarr(snap, log)
	if err != nil {
		return nil, err
	}
	// holder.Load is captured by the webhook reconcile loop closure;
	// globalLimiterPtr is shared with notifyTestContext. Both pointer
	// identities must stay stable across reload (OnApplied swaps the
	// inner cells, never the wrappers).
	holder := sonarrBundle.Holder
	globalLimiterPtr := sonarrBundle.GlobalLimiterPtr

	watchdogBundle, err := wiring.BuildWatchdog(persistence, sonarrBundle, log)
	if err != nil {
		return nil, err
	}
	// checker + wd are spawned on the lifecycle group below; the bundle
	// itself is also stored on the Server for Shutdown reach.
	checker := watchdogBundle.Checker
	wd := watchdogBundle.Watchdog

	rootCtx, rootCancel := context.WithCancel(ctx)
	defer func() {
		if armed {
			rootCancel()
		}
	}()

	// M-9: track every background goroutine for shutdown drain.
	// bgWG covers cross-package wiring that expects *sync.WaitGroup;
	// lifecycle owns inline goroutines spawned from Server.New.
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
	// txr feeds BuildEnrichment; scanUC + scanRepo are stored on the Server
	// for Shutdown's waitForScans; sweeper spawns on the lifecycle group.
	scanRepo := scanBundle.ScanRepo
	txr := scanBundle.Txr
	scanUC := scanBundle.ScanUC
	sweeper := scanBundle.Sweeper

	// seriesRepo / seriesCacheRepo / counterRepo are stateless GORM
	// wrappers — each call site gets its own. seriesCacheRepo +
	// counterRepo are still consumed by BuildHTTPServer below.
	seriesRepo := enrichpersistence.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	counterRepo := repositories.NewCounterRepository(db)

	webhookBundle, err := wiring.BuildWebhook(persistence, sonarrBundle, scanBundle, cfg, log)
	if err != nil {
		return nil, err
	}
	// reconciler + statusCache feed loops.NewWebhookReconcileLoop below.
	webhookReconciler := webhookBundle.Reconciler
	webhookStatusCache := webhookBundle.StatusCache

	instanceBundle, err := wiring.BuildInstance(persistence, webhookBundle, bus, log)
	if err != nil {
		return nil, err
	}

	regrabBundle, err := wiring.BuildRegrab(persistence, sonarrBundle, scanBundle, webhookBundle, &bgWG, log)
	if err != nil {
		return nil, err
	}
	// regrab loop owns the per-instance polling goroutines; SwapSettings
	// fires from the OnApplied fanout in wiring.StartSubscribers. Only
	// the rootCtx-bearing .Start lives here.
	regrabBundle.RegrabLoop.Start(rootCtx)

	torrentsyncBundle, err := wiring.BuildTorrentsync(
		persistence, sonarrBundle, scanBundle, webhookBundle, regrabBundle, &bgWG, log,
	)
	if err != nil {
		return nil, err
	}
	// torrentsync loop's Start needs rootCtx (owned by server.go, not
	// the wirer). Same pattern as regrabBundle.RegrabLoop.Start above.
	torrentsyncBundle.Loop.Start(rootCtx)

	extSvcBundle, err := wiring.BuildExtSvc(persistence, bootCfg, bus, log)
	if err != nil {
		return nil, err
	}
	// extSub is primed below so TMDB settings are available before
	// BuildEnrichment runs.
	extSub := extSvcBundle.Sub

	mediaBundle, err := wiring.BuildMedia(rootCtx, persistence, bootCfg, log)
	if err != nil {
		return nil, err
	}
	// Store + AssetsRepo feed the EnrichmentRepoBundle below; Handler is
	// the late-bind target for SetOnDemandFetcher.
	mediaStoreImpl := mediaBundle.Store
	mediaAssetsRepo := mediaBundle.AssetsRepo
	mediaHandler := mediaBundle.Handler

	seriesDetailBundle, err := wiring.BuildSeriesDetail(persistence, sonarrBundle, mediaBundle, bootCfg.Enrichment.MediaUnifiedResolve, log)
	if err != nil {
		return nil, err
	}
	// MediaResolver + PersonEnqueuerHolder are late-bound below once
	// BuildEnrichment has returned (the dispatcher / on-demand fetcher
	// don't exist at BuildSeriesDetail's call point).
	seriesDetailMediaResolver := seriesDetailBundle.MediaResolver
	peopleEnqueuerHolder := seriesDetailBundle.PersonEnqueuerHolder

	httpServer := wiring.BuildHTTPServer(
		persistence, runtimecfg, auth,
		sonarrBundle, watchdogBundle, scanBundle, webhookBundle,
		instanceBundle, regrabBundle, torrentsyncBundle, extSvcBundle,
		mediaBundle, seriesDetailBundle,
		seriesCacheRepo, counterRepo, log,
	)

	// Cooldown sweep — reload-aware cadence via SetInterval from the
	// OnApplied fan-out. Constructed in BuildScan; spawned here so
	// Shutdown can drain via the lifecycle group.
	lifecycle.Go(rootCtx, "cooldown-sweeper", func(ctx context.Context) {
		sweeper.Run(ctx)
	})

	// Phase 11 — webhook reconcile safety net (041d). The closure
	// over holder.Load is reload-aware: every publish swaps the inner
	// map so newly-added instances appear in the next tick.
	webhookReconcileLoopVal := loops.NewWebhookReconcileLoop(
		webhookReconciler,
		webhookStatusCache,
		holder.Load,
		log,
	)
	lifecycle.Go(rootCtx, "webhook-reconcile", func(ctx context.Context) {
		webhookReconcileLoopVal.Run(ctx)
	})

	// Story 202 (S-2) — prime extSub eagerly so TMDB settings are
	// available before BuildEnrichment runs. The boot publish below
	// triggers a second apply through the subscriber's bus channel.
	extSub.Start(rootCtx, nil)

	// Story 211 (C-2) repositories — fed to the enrichment dispatcher.
	seriesTextsRepo := repositories.NewSeriesTextsRepository(db)
	seasonsRepo := enrichpersistence.NewSeasonsRepository(db)
	episodesRepo := enrichpersistence.NewEpisodesRepository(db)
	episodeTextsRepo := repositories.NewEpisodeTextsRepository(db)
	peopleRepo := enrichpersistence.NewPeopleRepository(db)
	seriesPeopleRepo := enrichpersistence.NewSeriesPeopleRepository(db)
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
	personBiographiesRepo := enrichpersistence.NewPersonBiographiesRepository(db)
	personCreditsRepo := enrichpersistence.NewPersonCreditsRepository(db)
	coldStartScanner := wiring.NewColdStartScannerAdapter(seriesRepo)

	// Story 211 (C-2) — enrichment dispatcher. BuildEnrichment returns
	// a nil dispatcher when TMDB is disabled (boot stays green).
	enrichRepos := wiring.EnrichmentRepoBundle{
		Series:            seriesRepo,
		SeriesTexts:       seriesTextsRepo,
		Seasons:           seasonsRepo,
		Episodes:          episodesRepo,
		EpisodeTexts:      episodeTextsRepo,
		People:            peopleRepo,
		SeriesPeople:      seriesPeopleRepo,
		Genres:            wiring.GenresRepoAdapter{Main: genresRepo, I18n: genresI18nRepo},
		Keywords:          wiring.KeywordsRepoAdapter{Main: keywordsRepo, I18n: keywordsI18nRepo},
		Networks:          networksRepo,
		Companies:         companiesRepo,
		Videos:            wiring.VideosRepoAdapter{Inner: videosRepo},
		ContentRatings:    wiring.ContentRatingsRepoAdapter{Inner: contentRatingsRepo},
		ExternalIDs:       wiring.ExternalIDsRepoAdapter{Inner: externalIDsRepo},
		Recommendations:   recommendationsRepo,
		SyncLog:           syncLogRepo,
		PersonBiographies: personBiographiesRepo,
		PersonCredits:     wiring.PersonCreditsRepoAdapter{Inner: personCreditsRepo},
		ColdStartScanner:  coldStartScanner,
		LibraryWithIMDB:   wiring.NewOMDbBatchScannerAdapter(seriesRepo),
		MediaAssets:       mediaAssetsRepo,
		MediaStore:        mediaStoreImpl,
	}
	enrichBundle, err := wiring.BuildEnrichment(rootCtx, extSub, bootCfg, enrichRepos, txr, quotaCounter, log)
	if err != nil {
		return nil, fmt.Errorf("wire enrichment: %w", err)
	}

	// Story 352 — register OMDb + TMDB client reload subscribers on the
	// external services subscriber's listener fan-out. The OMDb holder
	// is allocated unconditionally so the subscriber is always wired;
	// TMDB holder is nil when TMDB was disabled at boot (in which case
	// the subscriber would only ever log the "boot_disabled" warn so we
	// skip registration to keep the log quiet).
	if enrichBundle != nil && enrichBundle.OMDbHolder != nil {
		omdbSub := adapters.NewOMDbClientSubscriber(enrichBundle.OMDbHolder,
			sharedports.DomainLogger(log, "omdb"))
		extSub.RegisterListener(infraextsvc.ServiceOMDB, omdbSub.Apply)
		// Prime by reading the current cached settings — the listener
		// fan-out only fires on future apply() calls, but Story 352
		// scenarios (operator change before any other reload) need an
		// initial seed so the "first apply with no prior baseline"
		// path is exercised in production exactly as in tests.
		omdbSub.Apply(rootCtx, extSub.Get(infraextsvc.ServiceOMDB))
	}
	if enrichBundle != nil && enrichBundle.TMDBHolder != nil {
		tmdbSub := adapters.NewTMDBClientSubscriber(enrichBundle.TMDBHolder, enrichBundle.TMDBFactoryCfg,
			sharedports.DomainLogger(log, "tmdb"))
		extSub.RegisterListener(infraextsvc.ServiceTMDB, tmdbSub.Apply)
		tmdbSub.Apply(rootCtx, extSub.Get(infraextsvc.ServiceTMDB))
	}

	// ───────── LATE BIND ZONE ─────────
	// The three callsites below need enrichBundle (its dispatcher /
	// fetcher / enqueuer don't exist at BuildSeriesDetail / BuildMedia
	// time). All holders are nil-safe so a disabled enrichment / media
	// subsystem keeps the boot path green.

	// Story 217 (H-2) — dispatcher into the people enqueuer holder.
	if enrichBundle != nil && enrichBundle.Dispatcher != nil {
		peopleEnqueuerHolder.Set(enrichBundle.Dispatcher)
	}

	// Story 316 — enqueuer + on-demand fetcher onto the MediaResolver.
	if enrichBundle != nil && seriesDetailMediaResolver != nil {
		var mediaEnq seriesdetail.MediaEnqueuer
		if enrichBundle.MediaEnqueuer != nil {
			mediaEnq = enrichBundle.MediaEnqueuer
		}
		seriesDetailMediaResolver.SetSideEffects(mediaEnq, enrichBundle.MediaOnDemand)
	}

	// Story 321 — on-demand fetcher onto the MediaHandler so cache-miss
	// reads can synchronously fill pending rows.
	if enrichBundle != nil && mediaHandler != nil && enrichBundle.MediaOnDemand != nil {
		mediaHandler.SetOnDemandFetcher(enrichBundle.MediaOnDemand)
	}
	// ───────── END LATE BIND ZONE ─────────

	// Boot scheduler — constructed after BuildEnrichment so the four
	// enrichment-derived job closures are ready. BuildScheduler Registers
	// every cron job before returning; caller owns Start() below.
	schedulerBundle, err := wiring.BuildScheduler(persistence, mediaBundle, cfg,
		wiring.SchedulerEnrichmentJobs{
			Nightly:          enrichBundle.Nightly,
			OMDbBudgetReset:  enrichBundle.OMDbBudgetReset,
			OMDbDailyBatch:   enrichBundle.OMDbDailyBatch,
			UsesQuotaCounter: enrichBundle.UsesQuotaCounter,
		}, log)
	if err != nil {
		return nil, err
	}
	bootScheduler := schedulerBundle.BootScheduler

	// AuthRuntime pointer feeds the reload subscriber (auth config swap).
	var authRuntimePtr *middleware.AuthRuntimePointer
	if h := httpServer.AuthHandler(); h != nil {
		authRuntimePtr = h.AuthRuntime()
	}

	subSched, subClients, err := wiring.StartSubscribers(
		rootCtx,
		&bgWG,
		bus,
		persistence,
		sonarrBundle,
		scanBundle,
		watchdogBundle,
		regrabBundle,
		torrentsyncBundle,
		schedulerBundle,
		wiring.SubscriberDeps{
			Snap:            snap,
			Engine:          httpServer.Engine(),
			AuthRuntimePtr:  authRuntimePtr,
			ClientSecretEnv: bootCfg.Auth.OIDCClientSecret,
		},
		log,
	)
	if err != nil {
		return nil, fmt.Errorf("start subscribers: %w", err)
	}

	oidcProviderSub := reload.NewOIDCProviderSubscriber(oidcCache, log)
	go oidcProviderSub.Run(rootCtx, bus, func() {})

	// Start the boot scheduler. Start(ctx, scanUC) internally
	// Register(ScanJobName) + StartRegistered.
	if bootScheduler != nil {
		if err := bootScheduler.Start(rootCtx, scanUC); err != nil {
			return nil, fmt.Errorf("start scheduler: %w", err)
		}
		if cfg.Cron.OnStart {
			go func() {
				if _, err := scanUC.Run(rootCtx, scan.TriggerStartup); err != nil && !errors.Is(err, scan.ErrScanAlreadyRunning) {
					bootLog.ErrorContext(rootCtx, "startup scan failed", slog.String("error", err.Error()))
				}
			}()
		}
	}

	// Story 212 + 318 — cold-start backfill loop. Re-sweeps every
	// ColdStartResweepInterval (default 60s) to pick up rows the
	// dispatcher had to drop on a saturated cold channel. Runs AFTER
	// dispatcher.Start + bootScheduler.Start so every consumer is alive.
	if enrichBundle != nil && enrichBundle.ColdStart != nil {
		lifecycle.Go(rootCtx, "cold-start-backfill", func(ctx context.Context) {
			enrichBundle.ColdStart(ctx)
		})
	}

	// Re-publish the boot snapshot now that subscribers are alive
	// — they all apply it once and increment their success metric.
	bus.Publish(rootCtx, snap)

	// No-op in production (testcontext_stub.go); E2E builds use it to
	// assert per-subscriber state.
	notifyTestContext(bus, subSched, subClients, authRuntimePtr, globalLimiterPtr, holder.Load, checker.Snapshot)

	// Disarm defers — Server owns bus + rootCancel until Shutdown.
	armed = false
	return &Server{
		log:          log,
		shutLog:      shutLog,
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

// Run fires OnReady, starts the HTTP server, and blocks until ctx is
// cancelled or the server returns. Always calls Shutdown before exit.
func (s *Server) Run(ctx context.Context) error {
	if s.onReady != nil {
		s.onReady(s.bus)
	}
	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- s.httpServer.Start() }()

	select {
	case <-ctx.Done():
		s.shutLog.Info("shutdown signal received")
	case err := <-serverErrCh:
		if err != nil {
			s.shutLog.Error("http server stopped", slog.String("error", err.Error()))
		}
	}
	return s.Shutdown(ctx)
}

// Shutdown drains HTTP, enrichment, scheduler, in-flight scans, and
// background goroutines, then closes the DB handle and the reload bus.
// Ordering matches the original runWithContext shutdown ladder.
func (s *Server) Shutdown(parentCtx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		s.shutLog.Error("http shutdown error", slog.String("error", err.Error()))
	}

	// Story 211 — stop dispatcher BEFORE scheduler so in-flight series
	// worker drains before cron tears down. Story 214 — drain media
	// pre-warm AFTER dispatcher closes (no more new enqueues).
	if s.enrichBundle != nil && s.enrichBundle.Dispatcher != nil {
		s.enrichBundle.Dispatcher.Close()
	}
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

	// M-9: drain goroutines before closing DB. lifecycle = the 5 inline
	// spawns; bgWG = cross-package wiring (scan.UseCase, regrabLoop,
	// torrentsyncLoop, StartSubscribers).
	if err := s.lifecycle.Drain(10 * time.Second); err != nil {
		s.shutLog.Warn("lifecycle drain timed out", slog.String("error", err.Error()))
	}
	drainBackground(s.bgWG, 10*time.Second, s.log)

	if sqlDB, err := s.persistence.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
	// Reload bus closes last — matches the original defer ordering.
	s.bus.Close()
	s.shutLog.Info("seasonfill stopped cleanly")
	return nil
}
