package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	infraextsvc "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	httpserver "github.com/alexmorbo/seasonfill/internal/shared/http/edge"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
	"github.com/alexmorbo/seasonfill/internal/shared/reload"
	"github.com/alexmorbo/seasonfill/internal/wiring"
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
	scanRepo     *catalogpersistence.ScanRepository
	enrichBundle *wiring.EnrichmentBundle
	subSched     *reload.SchedulerSubscriber
	persistence  *wiring.PersistenceBundle
	watchdog     *wiring.WatchdogBundle
	onReady      func(*runtime.Bus)
	// Story 528 — retained so Shutdown can stop the throttle sweep
	// goroutine. nil-safe (Close is idempotent).
	onDemandEnricherHolder *adapters.OnDemandEnricherHolder
	// Story 533 — retained so Shutdown can mark the freshener closed
	// (subsequent EnsureFresh calls return Fresh=true cheaply).
	seriesFreshenerHolder *adapters.SeriesFreshenerHolder
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
	seriesCacheRepo := catalogpersistence.NewSeriesCacheRepository(db, seriesRepo)
	counterRepo := catalogpersistence.NewCounterRepository(db)

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
	// Story 479b — watchdog state collector (5min cadence) publishing
	// cooldown_pending + blacklist_size gauges per known instance.
	bgWG.Add(1)
	go regrabBundle.WatchdogStateCollector.Run(rootCtx)

	torrentsyncBundle, err := wiring.BuildTorrentsync(
		persistence, sonarrBundle, scanBundle, webhookBundle, regrabBundle, &bgWG, log,
	)
	if err != nil {
		return nil, err
	}
	// torrentsync loop's Start needs rootCtx (owned by server.go, not
	// the wirer). Same pattern as regrabBundle.RegrabLoop.Start above.
	torrentsyncBundle.Loop.Start(rootCtx)
	// B-32 — qbit_torrents row-count collector. bgWG.Add + go-Run
	// mirrors the sweep/regrab pattern; the loop calls bgWG.Done on
	// exit. Drained by drainBackground at shutdown.
	bgWG.Add(1)
	go torrentsyncBundle.QbitCapacityLoop.Run(rootCtx)

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

	// httpServer is constructed AFTER BuildEnrichment + the discovery
	// runtime block below so the curated discovery handler (story 507
	// N-2f) — which depends on the worker's IsWarming/RefreshNow ports
	// — can be wired through the request chain. The late-bind zone
	// further down mutates handler internals AFTER BuildHTTPServer
	// returns; that pattern still holds because gin captures method
	// pointers and the struct's internal state is observed via
	// per-request dispatch.

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
	seriesTextsRepo := enrichpersistence.NewSeriesTextsRepository(db)
	seasonsRepo := enrichpersistence.NewSeasonsRepository(db)
	episodesRepo := enrichpersistence.NewEpisodesRepository(db)
	episodeTextsRepo := enrichpersistence.NewEpisodeTextsRepository(db)
	peopleRepo := enrichpersistence.NewPeopleRepository(db)
	genresRepo := enrichpersistence.NewGenresRepository(db)
	genresI18nRepo := enrichpersistence.NewGenresI18nRepository(db)
	keywordsRepo := enrichpersistence.NewKeywordsRepository(db)
	keywordsI18nRepo := enrichpersistence.NewKeywordsI18nRepository(db)
	networksRepo := enrichpersistence.NewNetworksRepository(db)
	companiesRepo := enrichpersistence.NewCompaniesRepository(db)
	videosRepo := enrichpersistence.NewVideosRepository(db)
	contentRatingsRepo := enrichpersistence.NewContentRatingsRepository(db)
	externalIDsRepo := enrichpersistence.NewExternalIDsRepository(db)
	recommendationsRepo := enrichpersistence.NewRecommendationsRepository(db)
	// 464b: D-3 failure tracking — workers + composer share one
	// EnrichmentErrorsRepository instance. Success is now stamped
	// directly on the canon row via Series.MarkTMDBSynced /
	// MarkOMDBSynced (people side: People.MarkSynced).
	enrichmentErrorsRepo := enrichpersistence.NewEnrichmentErrorsRepository(db)
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
		Genres:            wiring.GenresRepoAdapter{Main: genresRepo, I18n: genresI18nRepo},
		Keywords:          wiring.KeywordsRepoAdapter{Main: keywordsRepo, I18n: keywordsI18nRepo},
		Networks:          networksRepo,
		Companies:         companiesRepo,
		Videos:            wiring.VideosRepoAdapter{Inner: videosRepo},
		ContentRatings:    wiring.ContentRatingsRepoAdapter{Inner: contentRatingsRepo},
		ExternalIDs:       wiring.ExternalIDsRepoAdapter{Inner: externalIDsRepo},
		Recommendations:   recommendationsRepo,
		EnrichmentErrors:  enrichmentErrorsRepo,
		PersonBiographies: personBiographiesRepo,
		PersonCredits:     wiring.PersonCreditsRepoAdapter{Inner: personCreditsRepo},
		ColdStartScanner:  coldStartScanner,
		SeriesStaleScan:   wiring.NewSeriesStaleScanAdapter(seriesRepo),
		PeopleStaleScan:   wiring.NewPeopleStaleScanAdapter(peopleRepo),
		LibraryWithIMDB:   wiring.NewOMDbBatchScannerAdapter(seriesRepo),
		RefreshPicker:     wiring.NewRefreshPickerAdapter(seriesRepo), // Story 534
		MediaAssets:       mediaAssetsRepo,
		MediaStore:        mediaStoreImpl,
	}
	enrichBundle, err := wiring.BuildEnrichment(rootCtx, extSub, extSvcBundle.UC, bootCfg, enrichRepos, txr, quotaCounter, seriesDetailMediaResolver, log)
	if err != nil {
		return nil, fmt.Errorf("wire enrichment: %w", err)
	}

	// Story 352 — register OMDb + TMDB client reload subscribers on the
	// external services subscriber's listener fan-out. The OMDb holder
	// is allocated unconditionally so the subscriber is always wired;
	// TMDB holder is nil when TMDB was disabled at boot (in which case
	// the subscriber would only ever log the "boot_disabled" warn so we
	// skip registration to keep the log quiet).
	// 473 (B-25/B-24): OMDb subscriber now carries OnFirstActivation
	// so adding a key via UI on a boot-disabled instance fires the
	// daily-batch sweep immediately. Parallels TMDB Story 470 wiring.
	if enrichBundle != nil && enrichBundle.OMDbHolder != nil {
		omdbSub := adapters.NewOMDbClientSubscriber(enrichBundle.OMDbHolder,
			sharedports.DomainLogger(log, "omdb")).
			WithOnFirstActivation(enrichBundle.OMDbActivation).
			WithInitialActivated(enrichBundle.OMDbBootEnabled) // 482 (B-22): suppress prime-pass hook when boot already constructed the client
		extSub.RegisterListener(infraextsvc.ServiceOMDB, omdbSub.Apply)
		// Prime by reading the current cached settings — the listener
		// fan-out only fires on future apply() calls, but Story 352
		// scenarios (operator change before any other reload) need an
		// initial seed so the "first apply with no prior baseline"
		// path is exercised in production exactly as in tests.
		// 482 (B-22): boot-enabled installs seed activated=true via
		// WithInitialActivated above, so the prime-pass does NOT
		// re-fire OnFirstActivation (which would duplicate the boot
		// kick at enrichment.go:712). Runtime saves still fire it
		// normally — the flag toggles back to false on clear.
		omdbSub.Apply(rootCtx, extSub.Get(infraextsvc.ServiceOMDB))
	}
	// Story 352 + 470 (B-7) — TMDB reload subscriber. Always
	// registered now (the wiring layer always allocates the holder).
	// On a nil→non-nil client transition (operator saves the first
	// key at runtime) OnFirstActivation fires a one-shot cold-start
	// sweep so enrichment converges within ms of the save.
	if enrichBundle != nil && enrichBundle.TMDBHolder != nil {
		tmdbSub := adapters.NewTMDBClientSubscriber(enrichBundle.TMDBHolder, enrichBundle.TMDBFactoryCfg,
			sharedports.DomainLogger(log, "tmdb")).
			WithOnFirstActivation(enrichBundle.OnFirstActivation).
			WithInitialActivated(enrichBundle.TMDBBootEnabled) // 482 (B-22): suppress prime-pass hook when boot already constructed the client
		extSub.RegisterListener(infraextsvc.ServiceTMDB, tmdbSub.Apply)
		tmdbSub.Apply(rootCtx, extSub.Get(infraextsvc.ServiceTMDB))
	}

	// Story 497 (B-35) — boot-time validation of configured TMDB+OMDb
	// keys. Without this, a pod restart wipes the per-pod
	// `validationResults` sync.Map (Story 489 B-17), trapping the
	// onboarding stepper (Story 494 N-1d) at step 3/4 = "in_progress"
	// until the operator clicks "Test" in Settings. The boot kick
	// re-runs the same validate-on-save probe used by Upsert so the
	// stepper auto-advances to "done" (valid) / "error" (invalid_key)
	// within ~30s of pod ready. Async-from-caller's POV — the
	// errgroup inside the UC blocks the goroutine but not the boot
	// path. lifecycle.Go drains the goroutine cleanly on shutdown.
	lifecycle.Go(rootCtx, "extsvc-boot-validation", func(ctx context.Context) {
		extSvcBundle.UC.ValidateConfiguredKeysOnBoot(ctx)
	})

	// ───────── LATE BIND ZONE ─────────
	// The three callsites below need enrichBundle (its dispatcher /
	// fetcher / enqueuer don't exist at BuildSeriesDetail / BuildMedia
	// time). All holders are nil-safe so a disabled enrichment / media
	// subsystem keeps the boot path green.

	// Story 217 (H-2) — dispatcher into the people enqueuer holder.
	if enrichBundle != nil && enrichBundle.Dispatcher != nil {
		peopleEnqueuerHolder.Set(enrichBundle.Dispatcher)
	}

	// Story 528 — dispatcher into the on-demand enricher holder so the
	// TMDBFallbackUseCase can fire PriorityHot enrichment jobs when a
	// user opens a stub-canon detail page (Bug 1: empty /series/{id}
	// for Discovery-stubbed rows).
	if enrichBundle != nil && enrichBundle.Dispatcher != nil && seriesDetailBundle.OnDemandEnricherHolder != nil {
		seriesDetailBundle.OnDemandEnricherHolder.Set(enrichBundle.Dispatcher)
	}

	// Story 533 — series worker into the freshener holder. EnsureFresh
	// then runs SeriesWorker.Handle synchronously with a 3 s budget +
	// singleflight per (seriesID, lang) so a cold/stale detail open
	// completes data hydration in the SAME request rather than waiting
	// on the async dispatcher path. Reuses the SAME *SeriesWorker
	// pointer the dispatcher's series-worker goroutine consumes —
	// idempotent + safe.
	if enrichBundle != nil && enrichBundle.SeriesWorker != nil && seriesDetailBundle.SeriesFreshenerHolder != nil {
		seriesDetailBundle.SeriesFreshenerHolder.Set(enrichBundle.SeriesWorker)
	}

	// Story 316 — enqueuer + on-demand fetcher onto the MediaResolver.
	if enrichBundle != nil && seriesDetailMediaResolver != nil {
		var mediaEnq media.Enqueuer
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

	// Story 506 (N-2e) — DiscoveryWorker. BuildDiscoveryPersistence
	// supplies repos + stub adapter; BuildDiscoveryRuntime constructs
	// the Worker over the TMDBClientHolder so a runtime TMDB
	// disable/enable propagates through the holder's atomic.Pointer
	// without restarting the loop. The loop entry point in
	// cmd/server/loops fires the first Tick immediately (cold-start
	// per PRD §5.1.1 line 666) and ticks every 1h thereafter.
	discoPersistence, err := wiring.BuildDiscoveryPersistence(persistence, seriesRepo)
	if err != nil {
		return nil, fmt.Errorf("wire discovery persistence: %w", err)
	}
	var discoTMDB discoapp.TMDBClient
	if enrichBundle != nil && enrichBundle.TMDBHolder != nil {
		discoTMDB = enrichBundle.TMDBHolder
	}
	var discoRuntime *wiring.DiscoveryRuntimeBundle
	if discoTMDB != nil {
		discoRuntime, err = wiring.BuildDiscoveryRuntime(wiring.DiscoveryRuntimeDeps{
			Persistence: discoPersistence,
			DB:          db,
			TMDB:        discoTMDB,
			Log:         sharedports.DomainLogger(log, "discovery"),
		})
		if err != nil {
			return nil, fmt.Errorf("wire discovery runtime: %w", err)
		}
		lifecycle.Go(rootCtx, "discovery-worker", func(ctx context.Context) {
			loops.RunDiscovery(ctx, discoRuntime.Worker,
				loops.DefaultDiscoveryInterval,
				sharedports.DomainLogger(log, "discovery"))
		})
	}

	// Story 540 / B-49 — discovery genre catalog sync. Boot pass + 24h
	// ticker. Goroutine; never blocks server start. Without this loop
	// the discovery picker falls back to en-US for the ~10/18 TMDB TV
	// genres that the per-series enrichment worker has never touched in
	// ru-RU (and any future locale.SupportedUserLanguages addition).
	if discoRuntime != nil && enrichBundle != nil && enrichBundle.TMDBHolder != nil {
		gsBundle, gsErr := wiring.BuildDiscoveryGenreSync(wiring.DiscoveryGenreSyncDeps{
			Genres: genresRepo,
			I18n:   genresI18nRepo,
			TMDB:   enrichBundle.TMDBHolder,
			Log:    sharedports.DomainLogger(log, "discovery"),
		})
		if gsErr != nil {
			return nil, fmt.Errorf("wire discovery genre sync: %w", gsErr)
		}
		lifecycle.Go(rootCtx, "discovery-genre-sync", func(ctx context.Context) {
			loops.RunDiscoveryGenreSync(ctx, gsBundle.Syncer,
				loops.DefaultDiscoveryGenreSyncInterval,
				sharedports.DomainLogger(log, "discovery"))
		})
	}

	// Story 507 (N-2f) — curated discovery HTTP handler. Built only
	// when the discovery runtime exists (i.e. TMDB was constructable
	// at boot). When TMDB is runtime-only enabled (operator flips
	// from disabled→configured), the routes will be absent until pod
	// restart — explicit trade-off documented in 507's roadmap notes.
	//
	// Story 508 (N-2g) — search use case threads through here. tmdb /
	// stubs / dispatcher are derived from holders/bundles wired above.
	// Any nil → BuildDiscoveryHTTP returns a bundle whose SearchUC is
	// nil and the handler returns 503 search_unavailable.
	var discoveryHTTPBundle *wiring.DiscoveryHTTPBundle
	if discoRuntime != nil {
		var searchTMDB discoapp.SearchTMDB
		if enrichBundle != nil && enrichBundle.TMDBHolder != nil {
			searchTMDB = enrichBundle.TMDBHolder
		}
		var dispAdapter discoapp.EnrichmentDispatcher
		if enrichBundle != nil && enrichBundle.Dispatcher != nil {
			dispAdapter = &wiring.EnrichmentDispatcherAdapter{Inner: enrichBundle.Dispatcher}
		}
		discoveryHTTPBundle = wiring.BuildDiscoveryHTTP(
			persistence,
			discoRuntime,
			discoPersistence.ListRepo,
			searchTMDB,
			discoPersistence.Stubs,
			dispAdapter,
			seriesDetailMediaResolver, // story 526 — shared MediaResolver
			seriesCacheRepo,           // story 527 — in_library_instances batch lookup
			sharedports.DomainLogger(log, "discovery"),
		)
	}

	// Story 509 (N-2h) — /discovery/discover ad-hoc passthrough.
	// Built only when the discovery runtime exists. Routes auth-gated.
	var discoverBundle *wiring.DiscoveryDiscoverBundle
	if discoRuntime != nil && enrichBundle != nil && enrichBundle.TMDBHolder != nil {
		discoverBundle = wiring.BuildDiscoveryDiscover(wiring.DiscoveryDiscoverDeps{
			TMDBClient:       enrichBundle.TMDBHolder,
			Stubs:            discoPersistence.Stubs,
			Worker:           discoRuntime.Worker,
			Resolver:         seriesDetailMediaResolver, // story 526 — shared MediaResolver
			LibraryInstances: seriesCacheRepo,           // story 527 — in_library_instances batch lookup
			Log:              sharedports.DomainLogger(log, "discovery"),
		})
		if discoveryHTTPBundle != nil {
			discoveryHTTPBundle.DiscoverHandler = discoverBundle.Handler
		}
		lifecycle.Go(rootCtx, "discover-bg-fetcher", func(ctx context.Context) {
			if err := discoverBundle.BgFetcher.RunWorker(ctx); err != nil {
				sharedports.DomainLogger(log, "discovery").Error(
					"discover bg-fetcher exited with error",
					slog.String("error", err.Error()))
			}
		})
	}

	// Story 508 (N-2g / B-9 Scope A) — late-bind the ColdStartKicker's
	// OnSyncCompleted hook into the scan use case via
	// WithPostScanCycle. BuildScan ran earlier (before BuildEnrichment)
	// so the hook is wired here once enrichBundle exists. Builder
	// returns the same *UseCase pointer so the field swap is safe
	// at this point (no scan has started yet — the cron scheduler
	// fires later in Start()).
	if enrichBundle != nil && enrichBundle.ColdStartKicker != nil && scanUC != nil {
		scanUC.WithPostScanCycle(enrichBundle.ColdStartKicker.OnSyncCompleted)
	}

	// BuildHTTPServer now runs AFTER enrichBundle + discovery wirings
	// so the curated discovery handler can be threaded through. The
	// later late-bind zone still mutates already-registered handler
	// internals (mediaHandler.SetOnDemandFetcher etc.) — moved BELOW
	// here so the routes are registered before the LATE BIND mutations
	// kick in (these are no-ops from the route-registration POV; gin
	// captured method pointers).
	// Story 525: thread the TMDB holder into the metadata bundle so the
	// sonarr-lookup endpoint can resolve authoritative per-season
	// episode_count from our catalog / TMDB instead of Sonarr's stub
	// data (Sonarr returns 0 episodes for not-yet-added series).
	var tmdbSeasonsClient wiring.TMDBSeasonsClient
	if enrichBundle != nil && enrichBundle.TMDBHolder != nil {
		tmdbSeasonsClient = enrichBundle.TMDBHolder
	}
	httpServer := wiring.BuildHTTPServer(
		persistence, runtimecfg, auth,
		sonarrBundle, watchdogBundle, scanBundle, webhookBundle,
		instanceBundle, regrabBundle, torrentsyncBundle, extSvcBundle,
		mediaBundle, seriesDetailBundle,
		seriesCacheRepo, counterRepo, discoveryHTTPBundle, tmdbSeasonsClient, log,
	)

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

	// Story 534 — background tiered refresh scheduler. Gated by
	// cfg.Cron.Enabled so disabling cron globally also disables the
	// background refresh sweep (single operator lever). Skips when
	// the wirer did not construct a scheduler (RefreshPicker absent
	// from EnrichmentRepoBundle).
	if cfg.Cron.Enabled && enrichBundle != nil && enrichBundle.RefreshScheduler != nil {
		refreshLog := sharedports.DomainLogger(log, "enrichment")
		lifecycle.Go(rootCtx, "refresh-scheduler", func(ctx context.Context) {
			loops.RunRefresh(ctx, enrichBundle.RefreshScheduler,
				loops.DefaultRefreshInterval, refreshLog)
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
		log:                    log,
		shutLog:                shutLog,
		cfg:                    cfg,
		bus:                    bus,
		bgWG:                   &bgWG,
		lifecycle:              lifecycle,
		rootCancel:             rootCancel,
		httpServer:             httpServer,
		scanUC:                 scanUC,
		scanRepo:               scanRepo,
		enrichBundle:           enrichBundle,
		subSched:               subSched,
		persistence:            persistence,
		watchdog:               watchdogBundle,
		onReady:                opts.OnReady,
		onDemandEnricherHolder: seriesDetailBundle.OnDemandEnricherHolder,
		seriesFreshenerHolder:  seriesDetailBundle.SeriesFreshenerHolder,
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
	// Story 528 — stop the on-demand enricher throttle sweep goroutine.
	// Idempotent + nil-safe.
	if s.onDemandEnricherHolder != nil {
		s.onDemandEnricherHolder.Close()
	}
	// Story 533 — mark the freshener closed so any in-flight EnsureFresh
	// short-circuits to Fresh=true. Idempotent + nil-safe.
	if s.seriesFreshenerHolder != nil {
		s.seriesFreshenerHolder.Close()
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
