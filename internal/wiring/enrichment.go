package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/internal/config"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	appextsvc "github.com/alexmorbo/seasonfill/internal/enrichment/app/externalservices"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	enrichrest "github.com/alexmorbo/seasonfill/internal/enrichment/rest"
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/quota"
	infraextsvc "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/httpx"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// enrichment.go owns the wiring for the enrichment bounded context:
// the external-services runtime-config stack (Story 202 S-2), the
// TMDB/OMDb dispatcher pipeline (Stories 211/212/213), the people
// enrichment workers, the cold-start backfill loop (Story 318), and
// the repo→port adapter shims the application layer reads through.

// ExtSvcBundle groups the external-services runtime-config components
// constructed at boot. Returned by BuildExtSvc. Threaded into:
//
//   - httpserver.NewServer (Handler) — the HTTP wirer remains in
//     server.go for now.
//   - server.go calls Sub.Start(rootCtx, nil) directly because the
//     subscriber owner needs the cancellation-bearing rootCtx, which
//     the wirer does not (and should not) own.
//
// Field-level invariants:
//
//   - Sub is the runtime-config subscriber. Built FIRST with a nil use
//     case so it can be injected as the use case's Publisher; the use
//     case is then constructed and back-wired via Sub.SetUseCase before
//     callers see the bundle.
//
//   - UC owns the masked-DTO contract for the HTTP layer. Plaintext
//     keys never leave the subscriber/use case pair — the Handler
//     emits the masked DTO only.
//
//   - Handler is the HTTP adapter wrapping UC.
type ExtSvcBundle struct {
	Sub     *adapters.ExternalServicesSubscriber
	UC      *appextsvc.UseCase
	Handler *enrichrest.ExternalServicesHandler
}

// BuildExtSvc wires the Story 202 (S-2) external-services runtime-config
// stack. Construction order mirrors the pre-339 inline body verbatim:
//
//  1. Repository (cipher-wrapped settings repo backed by persistence.DB).
//  2. Subscriber (built with nil UC).
//  3. UseCase (subscriber injected as Publisher; bootCfg.ExternalServices
//     supplies the env lookup; production tester).
//  4. SetUseCase back-wires the subscriber.
//  5. Handler wraps the UC.
//
// Start(rootCtx, nil) is NOT called here — server.go owns rootCtx and
// fires the prime after Build returns, matching the original temporal
// position (before the wireEnrichment block).
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers.
func BuildExtSvc(
	persistence *PersistenceBundle,
	bootCfg *config.Bootstrap,
	bus *runtime.Bus,
	log *slog.Logger,
) (*ExtSvcBundle, error) {
	// F-4b-8: subscriber records describe configuration loading at boot
	// (cache prime + on-operator-change apply); admin tag covers the
	// operator-facing UC (TMDB/OMDb credential rotation). PRD §6.5.
	bootLog := sharedports.DomainLogger(log, "boot")
	adminLog := sharedports.DomainLogger(log, "admin")
	extRepo := infraextsvc.NewRepository(persistence.DB, persistence.Cipher)
	sub := adapters.NewExternalServicesSubscriber(bus, bootLog)
	uc := appextsvc.NewUseCase(extRepo, bootCfg.ExternalServices.Lookup(),
		appextsvc.NewRealTester(), sub, adminLog)
	sub.SetUseCase(uc)
	handler := enrichrest.NewExternalServicesHandler(uc, log)

	return &ExtSvcBundle{
		Sub:     sub,
		UC:      uc,
		Handler: handler,
	}, nil
}

// EnrichmentBundle groups the dispatcher + the nightly job closure
// so main.go's wiring stays a single call.
type EnrichmentBundle struct {
	Dispatcher *appenrich.DispatcherImpl
	Nightly    func(context.Context)
	// ColdStart (212) runs the one-shot series backfill — series
	// rows whose enrichment_tmdb_synced_at is NULL are enqueued at
	// PriorityCold. nil when enrichment is disabled.
	ColdStart func(context.Context)
	// 213 additions: nil when OMDb disabled / unconfigured.
	OMDbDailyBatch  func(context.Context)
	OMDbBudgetReset func(context.Context)
	// 214 (F-1) additions. Nil when TMDB is disabled — there's no
	// upstream to pre-warm so the producer/consumer pair is skipped.
	// MediaEnqueuer is the producer port for the series worker;
	// MediaDownloader is the consumer (Start in main.go after
	// dispatcher.Start, Close at shutdown).
	MediaEnqueuer   *appmedia.Enqueuer
	MediaDownloader *appmedia.Downloader
	// MediaHTTP is the TMDB-proxied HTTP client. Re-exported so the
	// HTTP MediaHandler in main.go uses the SAME proxy for its
	// lost-object refetch path.
	MediaHTTP *http.Client
	// Story 316 — on-demand fetcher for the seriesdetail.MediaResolver
	// first-fold path. Shares the Downloader's *rate.Limiter so the
	// global 5 rps cap covers both sync + async. nil-OK (the resolver
	// silently falls back to async-only).
	MediaOnDemand appmedia.OnDemandFetcher
	// 305: true when the OMDb budget guard is backed by the DB
	// QuotaCounter. main.go uses this to decide whether to register
	// the legacy `omdb-budget-reset` cron (in-process path) or
	// the daily `quota-counter-gc` cron (DB-backed path).
	UsesQuotaCounter bool
	// Story 352 — runtime-swappable client holders. server.go hands
	// each holder + its factory to the matching reload subscriber so a
	// settings change (UI Upsert) rebuilds the client in-place.
	// 470 (B-7): TMDBHolder is now ALWAYS non-nil. The inner client may
	// be nil at boot when TMDB is unconfigured; the reload subscriber
	// populates it on the operator's first key save and OnFirstActivation
	// fires a one-shot cold-start sweep so enrichment converges within
	// seconds of the save (rather than within ColdStartResweepInterval).
	//   - OMDbHolder nil  → never (always allocated; may be empty).
	TMDBHolder     *adapters.TMDBClientHolder
	OMDbHolder     *adapters.OMDbClientHolder
	TMDBFactoryCfg adapters.TMDBClientFactoryConfig
	// 470 (B-7): one-shot sweep callback invoked by the TMDB reload
	// subscriber on the first nil→non-nil client transition. nil when
	// the cold-start scanner repo is unavailable (defensive — current
	// production wiring always supplies it).
	OnFirstActivation func(context.Context)
	// 473 (B-25/B-24): OMDb activation callback. Invoked by the OMDb
	// reload subscriber on the first nil→non-nil client transition
	// (operator adds key via UI). Mirrors OnFirstActivation for TMDB.
	// Signature is (ctx, trigger) so the subscriber can pass a stable
	// "runtime_first_key_save" trigger string for log auditability.
	OMDbActivation func(ctx context.Context, trigger string)
	// 482 (B-22): boot-enabled flags consumed by server.go to seed the
	// reload subscribers with WithInitialActivated. True when the boot
	// path successfully constructed the matching client (env/DB key
	// present + factory succeeded). False otherwise — including when
	// the factory ran but failed, since a failed boot is NOT an
	// activation. When true, the subscriber's prime-pass Apply suppresses
	// the OnFirstActivation hook (which would duplicate the cold-start /
	// daily-batch sweeps the boot path already ran).
	TMDBBootEnabled bool
	OMDbBootEnabled bool
	// ColdStartKicker — story 508 (B-9 Scope A). Boot-race breaker.
	// nil when ColdStartScanner is unavailable (defensive — current
	// production wiring always supplies it). cmd/server/server.go
	// threads ColdStartKicker.OnSyncCompleted into scan.UseCase
	// via WithPostScanCycle so a scan_completed sweep kicks
	// BackfillSeries within ms when the boot pass scanned an empty
	// series table.
	ColdStartKicker *adapters.ColdStartKicker
	// SeriesWorker (Story 533) — exposed so cmd/server/server.go's LATE
	// BIND ZONE can wire it into SeriesFreshenerHolder for synchronous
	// read-through TMDB refresh on cold/stale detail opens. Same
	// dispatcher-bound instance the worker pool already consumes.
	SeriesWorker *appenrich.SeriesWorker
	// RefreshScheduler (Story 534) — background tiered refresh
	// scheduler. nil when RefreshPicker is absent OR Cron.Enabled gate
	// flips the loop off. server.go's LATE BIND ZONE owns the
	// lifecycle.Go("refresh-scheduler", ...) goroutine.
	RefreshScheduler *appenrich.RefreshScheduler
	// TVDBResolver (W15-13) — scan-piggyback tvdb→tmdb resolver, built
	// here where the concrete SeriesRepository + EnrichmentErrors ledger
	// + dispatcher + TMDB holder are all in scope. server.go's LATE BIND
	// ZONE wires it into scan.UseCase via WithTMDBResolver. nil when any
	// dependency is unavailable (resolver skips the piggyback).
	TVDBResolver *appenrich.TVDBResolver
	// OMDbWorker (W18-7a) — exposed so cmd/server/server.go's LATE BIND
	// ZONE can wire it as the on-view /ratings OMDb refresher. Same
	// budget/terminal/TTL/owner-write/journal instance the dispatcher's
	// OMDb handler consumes — reused wholesale (no duplication).
	OMDbWorker *appenrich.OMDbWorker
}

// BuildEnrichment builds the dispatcher + nightly stale scan closure.
// Always allocates the dispatcher, workers, holders, and media pipeline
// so a TMDB key added at runtime (Settings → External Services)
// activates enrichment without a process restart (Story 470 / B-7).
// The TMDB *http.Client itself is built only when the key is present at
// boot — runtime activation rebuilds it via the reload subscriber.
func BuildEnrichment(
	rootCtx context.Context,
	extSub *adapters.ExternalServicesSubscriber,
	extSvcUC *appextsvc.UseCase,
	bootstrap *config.Bootstrap,
	repos EnrichmentRepoBundle,
	tx appenrich.Transactor,
	quotaCounter quota.QuotaCounter,
	// mediaResolver — E-1 A4: shared *media.Resolver instance the composer
	// + discovery handler already hold. Threaded into SeriesWorkerDeps so
	// Worker.RefreshMediaAssets can mint eager sha256 hashes + write
	// media_assets pending rows inline (Story 347 unified-resolve contract).
	// nil-OK: A4 degrades to write raw paths + stamp only.
	mediaResolver *media.Resolver,
	log *slog.Logger,
) (*EnrichmentBundle, error) {
	// F-4b-5 / F-4b-7: three domain loggers wrapped once each per §6.5.
	// The enrichment context owns THREE logical buckets that anchor on
	// separate AllowedDomains slots:
	//   - enrichmentLog (domain="enrichment") — series/person hydration,
	//     cold-start backfill, dispatcher fan-out, nightly sweep, media
	//     pre-warm pipeline, wirer-local lifecycle records.
	//   - omdbLog (domain="omdb") — OMDb daily budget guard, OMDb
	//     worker, OMDb daily batch + budget reset cron closures.
	//   - tmdbLog (domain="tmdb") — F-4b-7 (Story 398). Threaded into
	//     TMDBClientFactoryConfig.Logger so BOTH the boot-path tmdb.New
	//     call AND every subsequent BuildTMDBClient rebuild via the
	//     Story 352 subscriber carry domain="tmdb" on the client's
	//     rate-limiter pause/resume INFO records.
	enrichmentLog := sharedports.DomainLogger(log, "enrichment")
	omdbLog := sharedports.DomainLogger(log, "omdb")
	tmdbLog := sharedports.DomainLogger(log, "tmdb")

	// 470 (B-7): no boot-time short-circuit. The dispatcher + holders +
	// workers are always wired. When TMDB is disabled at boot, the
	// holder simply carries a nil client until the reload subscriber
	// populates it on the operator's first key save. The worker layer
	// is client-nil-tolerant — GetTV/GetPerson/GetSeason on an empty
	// holder return ErrTMDBClientNotReady, which handleTMDBError
	// journals to enrichment_errors with a backoff. No panic, no leak.
	settings := extSub.Get(infraextsvc.ServiceTMDB)
	enabledAtBoot := settings.Enabled && settings.APIKey != ""
	if !enabledAtBoot {
		enrichmentLog.InfoContext(rootCtx, "enrichment.boot.tmdb_unconfigured",
			slog.Bool("enabled", settings.Enabled),
			slog.Bool("api_key", settings.APIKey != ""),
			slog.String("note", "workers wired; awaiting runtime activation via Settings → External Services"),
		)
	}

	// 470 (B-7): httpClient + tmdbClient only when enabled at boot.
	// Media pipeline depends on httpClient (it shares the proxy/TLS
	// pool with the TMDB API client) so the media pre-warm pipeline
	// stays off until the key is set. The reload subscriber rebuilds
	// the TMDB API client on key save — image.tmdb.org downloads
	// remain disabled until the next process restart (a known
	// limitation; the Series Detail UI's on-demand fetcher path falls
	// back to async download once the pipeline is up).
	var (
		httpClient      *http.Client
		tmdbClient      *tmdb.Client
		mediaEnqueuer   *appmedia.Enqueuer
		mediaDownloader *appmedia.Downloader
		mediaOnDemand   appmedia.OnDemandFetcher
		mediaPrewarmer  appenrich.MediaPrewarmer // nil OK
	)
	// Story 489 (B-17) — extSvcUC implements tmdb.AuthFailureReporter.
	// Wiring it into the factory means every TMDB client (boot path +
	// every reload-rebuilt instance) reports 401s back to the use case
	// so the operator-facing /external-services List + Dashboard banner
	// surface the invalid-key signal without a pod restart.
	tmdbFactoryCfg := adapters.TMDBClientFactoryConfig{
		Language:            tmdb.DefaultLanguage,
		RPS:                 bootstrap.ExternalServices.TMDBAPIRPS,
		Logger:              tmdbLog,
		AuthFailureReporter: extSvcUC,
		QuotaCounter:        quotaCounter, // B-1 — nil-OK; observability-only (TMDB has no daily cap)
	}
	if enabledAtBoot {
		var err error
		httpClient, err = infraextsvc.HttpClientFor(settings)
		if err != nil {
			return nil, err
		}
		// Story 312: surface the configured TMDB external-services proxy
		// at boot so the operator can confirm image.tmdb.org goes
		// through the same proxy as api.themoviedb.org (RU DPI blocks
		// both the same way).
		proxy := "none"
		if settings.ProxyURL != "" {
			proxy = settings.ProxyURL
		}
		enrichmentLog.InfoContext(rootCtx, "media.http_client.configured",
			slog.String("proxy", proxy),
		)

		// Story 313 + Story 346 — plumb SEASONFILL_TMDB_API_RPS (legacy
		// SEASONFILL_TMDB_RPS still honoured by config.FromEnv as an
		// alias) + the wiring logger so adaptive pause + resume INFO
		// lines surface under the enrichment component prefix. 0 from
		// config means "tmdb package picks its default (50 rps)".
		if os.Getenv("SEASONFILL_TMDB_API_RPS") == "" && os.Getenv("SEASONFILL_TMDB_RPS") != "" {
			enrichmentLog.WarnContext(rootCtx, "config.deprecated_env",
				slog.String("env", "SEASONFILL_TMDB_RPS"),
				slog.String("replacement", "SEASONFILL_TMDB_API_RPS"),
				slog.String("removal", "next release"))
		}

		// Story 351 — tmdb.New must run FIRST. The TMDB client constructs
		// an internal CLONE of httpClient and wraps its Transport with
		// httpx.NewMetricsTransport("tmdb", ...). That clone captures
		// the CURRENT httpClient.Transport (the raw proxy transport).
		//
		// AFTER tmdb.New returns we mutate the SHARED httpClient pointer
		// in place — wrapping its Transport with httpx.NewMetricsTransport
		// ("tmdb_cdn", ...) — so every subsequent http.Request issued via
		// the shared pointer (i.e. every image.tmdb.org fetch from the
		// media downloader / on-demand fetcher) flows through the
		// "tmdb_cdn" metric writes.
		//
		// This ordering guarantees api.themoviedb.org metrics carry ONLY
		// client="tmdb" and image.tmdb.org metrics carry ONLY
		// client="tmdb_cdn" — no double-write.
		//
		// Story 352 — the factory mirrors this boot path verbatim so the
		// reload subscriber can rebuild a metric-wrapped TMDB API client
		// on key/proxy change. The downloader's "tmdb_cdn" wrap is NOT
		// rebuilt by the subscriber because the downloader was
		// constructed with the SHARED httpClient pointer below.
		tmdbClient, err = tmdb.New(tmdb.Config{
			Token:               settings.APIKey,
			HTTPClient:          httpClient,
			Language:            tmdbFactoryCfg.Language,
			RPS:                 tmdbFactoryCfg.RPS,
			Logger:              tmdbFactoryCfg.Logger,
			AuthFailureReporter: tmdbFactoryCfg.AuthFailureReporter, // 489 (B-17)
			QuotaCounter:        tmdbFactoryCfg.QuotaCounter,        // B-1
		})
		if err != nil {
			return nil, err
		}
		httpClient.Transport = httpx.NewMetricsTransport("tmdb_cdn", httpx.TMDBCDNEndpointFor, httpClient.Transport)

		// 214 (F-1): media pre-warm pipeline. Only constructed when both
		// the blob store + the media_assets repo are available; the pair
		// is required for the downloader to make persistent progress.
		// httpClient is SHARED with tmdbClient above so the same
		// proxy-connection pool serves both API + image fetches.
		if repos.MediaAssets != nil && repos.MediaStore != nil {
			mediaEnqueuer = appmedia.NewEnqueuer(enrichmentLog)
			// Story 346: split CDN limiter from the TMDB API limiter.
			mediaDownloader, err = appmedia.NewDownloader(mediaEnqueuer, appmedia.DownloaderDeps{
				Store:           repos.MediaStore,
				Repo:            repos.MediaAssets,
				HTTPClient:      httpClient,
				Logger:          enrichmentLog,
				CDNRateLimitRPS: bootstrap.ExternalServices.TMDBCDNRPS,
			})
			if err != nil {
				return nil, fmt.Errorf("media downloader: %w", err)
			}
			mediaPrewarmer = mediaPrewarmerAdapter{eq: mediaEnqueuer}
			// Story 316 — on-demand fetcher shares the downloader's rate
			// limiter so the 5 rps cap applies globally across the sync +
			// async paths.
			mediaOnDemand, err = appmedia.NewOnDemandFetcher(appmedia.OnDemandDeps{
				Store:      repos.MediaStore,
				Repo:       repos.MediaAssets,
				HTTPClient: httpClient,
				Limiter:    mediaDownloader.Limiter(),
				Logger:     enrichmentLog,
			})
			if err != nil {
				return nil, fmt.Errorf("media ondemand fetcher: %w", err)
			}
		}
	}

	// 470 (B-7): tmdbHolder is ALWAYS allocated. tmdbClient may be nil
	// at this point (boot-disabled); the reload subscriber populates
	// the holder on the operator's first key save. Workers receive
	// the holder, which satisfies appenrich.TMDBClient and returns
	// ErrTMDBClientNotReady on every method when the inner pointer
	// is nil — see cmd/server/adapters/extsvc_client_holders.go:102.
	tmdbHolder := adapters.NewTMDBClientHolder()
	if tmdbClient != nil {
		tmdbHolder.Set(tmdbClient)
	}

	// Story 212: dispatcherHolder breaks the construction cycle
	// (series worker needs Dispatcher seam → dispatcher needs both
	// handlers). The holder is handed to the series worker; the
	// real *DispatcherImpl is plugged into it after both workers
	// + dispatcher have been constructed. Calls before the plug-in
	// would no-op safely, but in this flow nothing fires before
	// dispatcher.Start.
	holder := &dispatcherHolder{}

	// Story 352 — workers receive the holder (not the bare client) so
	// the reload subscriber can swap the underlying *tmdb.Client without
	// rebuilding the worker. The holder satisfies appenrich.TMDBClient
	// via a thin atomic.Pointer indirection (one extra load per call —
	// negligible vs the network round-trip).
	// Story 533c: Languages left empty → constructor seeds with
	// locale.SupportedUserLanguages (currently en-US + ru-RU). PersonWorker
	// (below) keeps its single-language path because biographies remain
	// en-US-only until a follow-up story.
	worker, err := appenrich.NewSeriesWorker(appenrich.SeriesWorkerDeps{
		TMDB:               tmdbHolder,
		Tx:                 tx,
		Series:             repos.Series,
		SeriesTexts:        repos.SeriesTexts,
		Seasons:            repos.Seasons,
		Episodes:           repos.Episodes,
		EpisodeTexts:       repos.EpisodeTexts,
		SeasonTexts:        repos.SeasonTexts,      // B3b (Story 581) — nil-OK
		SeriesMediaTexts:   repos.SeriesMediaTexts, // C-posters-A (Story 584a) — nil-OK
		SeasonMediaTexts:   repos.SeasonMediaTexts, // S-C2 — nil-OK
		People:             repos.People,
		PersonCredits:      repos.PersonCredits,
		PersonCreditsTexts: repos.PersonCreditsTexts, // S-G — nil-OK
		Genres:             repos.Genres,
		Keywords:           repos.Keywords,
		Networks:           repos.Networks,
		Companies:          repos.Companies,
		Videos:             repos.Videos,
		ContentRatings:     repos.ContentRatings,
		ExternalIDs:        repos.ExternalIDs,
		Recommendations:    repos.Recommendations,
		EnrichmentErrors:   repos.EnrichmentErrors,
		// Story 571 B-54: rec children's canon poster/backdrop overwrite writer.
		// Same *SeriesRepository already provides SeriesRepo above; the bundle
		// exposes RecCanonWriter as its own field so main.go can pass the
		// concrete repository (which now carries UpdateRecCanonMedia) without
		// widening the SeriesRepo port surface.
		RecCanonWriter: repos.SeriesRecCanon,
		MediaPrewarmer: mediaPrewarmer, // 214 (F-1): nil-OK when MediaStore/MediaAssets absent
		MediaResolver:  mediaResolver,  // E-1 A4: shared *media.Resolver instance
		Dispatcher:     holder,
		Logger:         enrichmentLog,
	})
	if err != nil {
		return nil, err
	}

	// 212: person worker — REUSES the SAME tmdbHolder pointer
	// constructed above. The holder shares the live *tmdb.Client between
	// both workers; the limiter's 5-rps token bucket stays unified.
	// A second tmdb.New(...) in this function would fragment the bucket
	// and let the worker pool burst at 10 rps. NEVER call tmdb.New
	// again in this function — Story 352's reload subscriber Swap()s the
	// holder's inner pointer atomically, which preserves the single-
	// limiter invariant across rebuilds (the previous client is Close()d
	// after a drain delay).
	personWorker, err := appenrich.NewPersonWorker(appenrich.PersonWorkerDeps{
		TMDB:              tmdbHolder,
		Tx:                tx,
		Language:          tmdb.DefaultLanguage,
		People:            repos.People,
		PersonBiographies: repos.PersonBiographies,
		PersonCredits:     repos.PersonCredits,
		ExternalIDs:       repos.ExternalIDs,
		EnrichmentErrors:  repos.EnrichmentErrors,
		Logger:            enrichmentLog,
	})
	if err != nil {
		return nil, err
	}

	// 213 (D-1) — OMDb client + budget + worker. Story 352 — the holder
	// is allocated unconditionally so the reload subscriber can lift the
	// worker from "disabled at boot" → "enabled by operator" without a
	// process restart. When OMDb is disabled at boot the holder is empty
	// and the dispatcher's EntityOMDb goroutine logs "handler_nil" on
	// every dequeue until the subscriber populates it.
	omdbHolder := adapters.NewOMDbClientHolder()
	// 305: Story 305 — DB-backed quota counter when available; fall back
	// to in-process for backward-compat (and as a degrade path when the
	// DB row is unreadable). quotaCounter is nil when main.go fails to
	// construct the repo (defensive).
	//
	// Story 352 — budget + worker are constructed unconditionally so the
	// reload subscriber can flip OMDb from disabled→enabled at runtime
	// without touching the dispatcher. When the holder is empty the
	// worker's getter returns nil and the dequeue logs "handler_nil".
	var omdbBudget *appenrich.OMDbBudgetGuard
	if quotaCounter != nil {
		omdbBudget = appenrich.NewOMDbBudgetGuardDB(
			appenrich.DefaultOMDbBudget, quotaCounter, omdbLog, nil)
	} else {
		omdbBudget = appenrich.NewOMDbBudgetGuard(appenrich.DefaultOMDbBudget)
	}
	omdbWorker, err := appenrich.NewOMDbWorker(appenrich.OMDbWorkerDeps{
		Client:           omdbHolder.Get,
		Budget:           omdbBudget,
		Tx:               tx,
		Series:           repos.Series,
		EnrichmentErrors: repos.EnrichmentErrors,
		Logger:           omdbLog,
	})
	if err != nil {
		return nil, fmt.Errorf("new omdb worker: %w", err)
	}
	omdbWorkerHandle := func(ctx context.Context, id int64) error {
		return omdbWorker.Handle(ctx, domain.SeriesID(id))
	}
	// 473 (B-25/B-24): omdbDailyBatch + omdbBudgetReset closures are now
	// ALWAYS constructed. The closures runtime-gate on holder/budget
	// presence so a still-empty OMDb installation (key not yet set OR
	// cleared at runtime) no-ops the sweep without burning budget OR
	// log noise. Pre-473: closures were nil-when-disabled-at-boot, which
	// meant the cron either never registered (B-24 runtime-enable path)
	// OR registered but operator waited ~15h for next 04:30 UTC tick
	// (B-25 boot-enabled path). Mirrors Story 470 TMDB always-allocate.
	omdbSettings := extSub.Get(infraextsvc.ServiceOMDB)
	omdbEnabledAtBoot := omdbSettings.Enabled && omdbSettings.APIKey != ""
	if omdbEnabledAtBoot {
		omdbClient, err := adapters.BuildOMDbClient(omdbSettings)
		if err != nil {
			return nil, err
		}
		omdbHolder.Set(omdbClient)
	} else {
		omdbLog.InfoContext(rootCtx, "enrichment.omdb.disabled",
			slog.Bool("enabled", omdbSettings.Enabled),
			slog.Bool("api_key", omdbSettings.APIKey != ""),
			slog.String("note", "worker wired; awaiting runtime activation via Settings → External Services"),
		)
	}

	dispatcher := appenrich.NewDispatcher(appenrich.Workers{
		SeriesHandler: func(ctx context.Context, id int64) error {
			return worker.Handle(ctx, domain.SeriesID(id))
		},
		PersonHandler: personWorker.Handle,
		// Story 352 — omdbWorkerHandle is unconditional; the worker
		// itself short-circuits to "handler_nil" when the holder is
		// empty (OMDb disabled at boot, awaiting operator enable).
		OMDbHandler: omdbWorkerHandle,
	}, enrichmentLog)
	holder.set(dispatcher)

	// 473: omdbDailyBatch always non-nil. Runtime-gates on holder.
	omdbDailyBatch := func(ctx context.Context) {
		if omdbHolder.Load() == nil {
			omdbLog.DebugContext(ctx, "enrichment.omdb.daily_batch.skipped",
				slog.String("reason", "holder_empty_no_runtime_client"))
			return
		}
		if repos.LibraryWithIMDB == nil {
			omdbLog.WarnContext(ctx, "enrichment.omdb.daily_batch.no_scanner")
			return
		}
		const batchLimit = 900
		ids, err := repos.LibraryWithIMDB.ListLibraryWithIMDBStale(ctx, batchLimit)
		if err != nil {
			omdbLog.WarnContext(ctx, "enrichment.omdb.daily_batch.scan_failed",
				slog.String("error", err.Error()))
			return
		}
		for _, id := range ids {
			dispatcher.Enqueue(appenrich.EntityOMDb, id, appenrich.PriorityCold)
		}
		omdbLog.InfoContext(ctx, "enrichment.omdb.daily_batch.enqueued",
			slog.Int("series_count", len(ids)),
			slog.Int("quota_remaining", omdbBudget.Remaining()))
	}

	// 473: omdbBudgetReset stays unconditional too. In-process budget
	// guard owns its own zero-state — Reset on empty counter is harmless.
	// The DB-backed guard rotates at UTC midnight implicitly so this
	// closure goes unused there (bootstrap.go guard via UsesQuotaCounter).
	omdbBudgetReset := func(ctx context.Context) {
		before := omdbBudget.Remaining()
		omdbBudget.Reset()
		omdbLog.InfoContext(ctx, "enrichment.omdb.budget.reset",
			slog.Int("before", before),
			slog.Int("after", omdbBudget.Remaining()))
	}

	// 473 (B-25/B-24): OMDb activation kick. Unlike TMDB, OMDb has NO
	// periodic ticker — its only production driver was the daily cron at
	// 04:30 UTC. This closure runs one sweep (functionally identical to
	// omdbDailyBatch but with an "activation" log tag) to:
	//   - Boot-enabled (B-25): fire immediately at boot so a fresh deploy
	//     populates OMDb data within seconds (not 15h).
	//   - Runtime-enabled (B-24): fire on OMDb subscriber's first
	//     nil→non-nil holder transition so adding a key via UI does
	//     not require pod restart.
	// Idempotent — dispatcher.Enqueue dedup'd against in-flight slots.
	omdbActivation := func(ctx context.Context, trigger string) {
		if omdbHolder.Load() == nil {
			omdbLog.DebugContext(ctx, "enrichment.omdb.activation.skipped",
				slog.String("reason", "holder_empty"),
				slog.String("trigger", trigger))
			return
		}
		omdbLog.InfoContext(ctx, "enrichment.omdb.activation.triggered",
			slog.String("trigger", trigger))
		omdbDailyBatch(ctx)
	}

	nightly := func(ctx context.Context) {
		runNightlyTick(ctx, nightlyTickDeps{
			TMDBHolder:       tmdbHolder,
			SeriesStaleScan:  repos.SeriesStaleScan,
			PeopleStaleScan:  repos.PeopleStaleScan,
			EnrichmentErrors: repos.EnrichmentErrors,
			Dispatcher:       dispatcher,
			Log:              enrichmentLog,
		})
	}

	// 212 + 318: cold-start backfill closure. Hands repos.ColdStartScanner
	// + dispatcher to the application-layer loop; safe to call from a
	// background goroutine in main.go AFTER dispatcher.Start. The loop
	// re-sweeps every Enrichment.ColdStartResweepInterval (default 60s)
	// so series the dispatcher dropped on a saturated cold channel are
	// re-enqueued on the next tick. RunBackfillLoop returns when ctx
	// is Done.
	//
	// 470 (B-7): coldStart is the production driver — registers the
	// re-sweep ticker AND runs a one-shot canon-images recovery. The
	// inner sweep gates on tmdbHolder.Load() != nil per tick so a
	// boot-disabled instance does NOT spam pointless enrichment_errors
	// rows while the operator is still on the Settings page. Once the
	// holder is populated the gate opens automatically (atomic load).
	resweepInterval := bootstrap.Enrichment.ColdStartResweepInterval
	coldStart := func(ctx context.Context) {
		if repos.ColdStartScanner == nil {
			return
		}
		appenrich.RunBackfillLoop(ctx, repos.ColdStartScanner, dispatcher, resweepInterval,
			enrichmentLog, appenrich.RunBackfillLoopOptions{
				// 470 (B-7): per-tick gate so a still-empty TMDB holder
				// skips a sweep cycle without burning enrichment_errors rows.
				// The check is one atomic.Load — negligible cost.
				ShouldSweep: func() bool { return tmdbHolder.Load() != nil },
			})
	}

	// 508 (B-9 Scope A): ColdStartKicker breaks the boot race where
	// BackfillSeries runs BEFORE sonarr_sync populates series. Armed
	// on the first empty pass; fired on the next scan_completed sweep.
	// nil when ColdStartScanner is unavailable so the kicker is purely
	// optional from server.go's POV.
	var coldStartKicker *adapters.ColdStartKicker
	if repos.ColdStartScanner != nil {
		coldStartKicker = adapters.NewColdStartKicker(
			func(ctx context.Context) error {
				return appenrich.BackfillSeries(ctx, repos.ColdStartScanner, dispatcher, enrichmentLog)
			},
			enrichmentLog,
		)
		// Wrap `coldStart` so the boot pass's series count is reported
		// to the kicker BEFORE the unconditional original sweep runs.
		// A cheap LIMIT 1 read is enough for the armed/not-armed
		// decision — MarkPassResult is single-shot, so subsequent
		// 60s ticks do NOT re-arm the kicker. Failure of the pre-sweep
		// count is non-fatal; we fall through to the original sweep
		// unchanged.
		originalColdStart := coldStart
		coldStart = func(ctx context.Context) {
			ids, scanErr := repos.ColdStartScanner.ListMissingTMDBSync(ctx, 1)
			if scanErr == nil {
				coldStartKicker.MarkPassResult(len(ids))
			} else {
				enrichmentLog.WarnContext(ctx, "enrichment.cold_start_kicker.pre_sweep_count_failed",
					slog.String("error", scanErr.Error()))
			}
			originalColdStart(ctx)
		}
	}

	// 470 (B-7): onFirstActivation runs ONE cold-start sweep
	// immediately after the operator's first key save. The subscriber
	// fires this exactly once per nil→non-nil transition (and again
	// after a clear→re-set). Without it the operator must wait up to
	// ColdStartResweepInterval (60s default) for the periodic loop's
	// next tick. With it the sweep starts within ms of the save.
	// Safe to call from the subscriber goroutine — BackfillSeries is
	// itself thread-safe and uses the dispatcher's enqueue dedup.
	onFirstActivation := func(ctx context.Context) {
		if repos.ColdStartScanner == nil {
			return
		}
		enrichmentLog.InfoContext(ctx, "enrichment.runtime_activation.cold_start.triggered",
			slog.String("trigger", "tmdb_first_key_save"))
		if err := appenrich.BackfillSeries(ctx, repos.ColdStartScanner, dispatcher, enrichmentLog); err != nil {
			enrichmentLog.WarnContext(ctx, "enrichment.runtime_activation.cold_start.failed",
				slog.String("error", err.Error()))
		}
	}

	dispatcher.Start(rootCtx)
	// 214 (F-1): start the media downloader workers AFTER the
	// dispatcher is up so the first pre-warm enqueue always lands
	// on a live consumer. Close is driven from main.go's shutdown
	// path (Enqueuer.Close → Downloader.Close).
	if mediaDownloader != nil {
		mediaDownloader.Start(rootCtx)
	}

	// 473 (B-25): boot kick for OMDb if enabled. Runs the daily-batch
	// sweep ONCE at boot so the operator does not wait ~15h until next
	// 04:30 UTC cron tick. Cron stays registered for steady-state daily
	// refresh; boot kick handles the cold-start gap. Async to avoid
	// blocking the boot path on a 900-row enqueue scan.
	if omdbEnabledAtBoot {
		go omdbActivation(rootCtx, "boot_kick")
	}

	// Story 534 — background refresh scheduler. Build only when
	// repos.RefreshPicker is supplied (production wiring in main.go).
	// Tests/legacy callers that pass an empty bundle stay scheduler-less
	// — server.go's LATE BIND ZONE skips the lifecycle.Go goroutine
	// when the bundle field is nil.
	var refreshScheduler *appenrich.RefreshScheduler
	if repos.RefreshPicker != nil {
		rs, err := appenrich.NewRefreshScheduler(appenrich.RefreshSchedulerDeps{
			Picker:  repos.RefreshPicker,
			Worker:  seriesWorkerForceAdapter{inner: worker},
			Metrics: observability.NewEnrichmentRefreshMetrics(),
			Logger:  enrichmentLog,
		})
		if err != nil {
			return nil, fmt.Errorf("wire refresh scheduler: %w", err)
		}
		refreshScheduler = rs
	}

	// W15-13: scan-piggyback tvdb→tmdb resolver. Built here where the
	// concrete SeriesRepository (satisfies TVDBResolverSeriesRepo), the
	// EnrichmentErrors cooldown ledger, the dispatcher, and the TMDB
	// holder are all in scope. server.go wires it into scan.UseCase.
	// nil when any dependency is unavailable.
	var tvdbResolver *appenrich.TVDBResolver
	if seriesResolveRepo, ok := repos.Series.(appenrich.TVDBResolverSeriesRepo); ok &&
		repos.EnrichmentErrors != nil && dispatcher != nil && tmdbHolder != nil {
		tvdbResolver = appenrich.NewTVDBResolver(
			seriesResolveRepo,
			tmdbHolder,
			repos.EnrichmentErrors,
			dispatcher,
			nil, 0, // default clock + cooldown
			enrichmentLog,
		)
	}

	return &EnrichmentBundle{
		Dispatcher:        dispatcher,
		Nightly:           nightly,
		ColdStart:         coldStart,
		OMDbDailyBatch:    omdbDailyBatch,
		OMDbBudgetReset:   omdbBudgetReset,
		MediaEnqueuer:     mediaEnqueuer,
		MediaDownloader:   mediaDownloader,
		MediaOnDemand:     mediaOnDemand,
		MediaHTTP:         httpClient,
		UsesQuotaCounter:  quotaCounter != nil, // 473: holder-runtime-gate frees us from omdbEnabledAtBoot here
		TMDBHolder:        tmdbHolder,
		OMDbHolder:        omdbHolder,
		TMDBFactoryCfg:    tmdbFactoryCfg,
		OnFirstActivation: onFirstActivation, // 470 (B-7)
		OMDbActivation:    omdbActivation,    // 473 (B-25/B-24)
		// 482 (B-22): true iff the boot path constructed a live client.
		// Threaded into the reload subscribers' WithInitialActivated so
		// the prime-pass Apply does NOT re-fire the first-activation
		// hook (boot already triggered the cold-start / daily-batch sweep).
		TMDBBootEnabled: tmdbClient != nil,
		OMDbBootEnabled: omdbHolder.Load() != nil,
		// 508 (B-9 Scope A): nil-OK; server.go wires it to scan.UseCase
		// via WithPostScanCycle only when non-nil.
		ColdStartKicker: coldStartKicker,
		// 533: exposed so the SeriesDetail freshener holder can call
		// Handle() synchronously for cold/stale detail opens. Same
		// pointer the dispatcher's series-worker goroutine consumes.
		SeriesWorker: worker,
		// 534: background tiered refresh scheduler. nil when
		// repos.RefreshPicker is absent (defensive — production always
		// supplies it).
		RefreshScheduler: refreshScheduler,
		// W15-13: scan-piggyback tvdb→tmdb resolver (nil-OK).
		TVDBResolver: tvdbResolver,
		// W18-7a: on-view /ratings OMDb refresher (reused worker).
		OMDbWorker: omdbWorker,
	}, nil
}

// nightlyTickDeps groups the dependencies of the nightly stale-scan tick
// so the tick body can live outside BuildEnrichment for unit testing.
// All fields are non-nil contracts; the gate at the top short-circuits
// before any field is touched, so the gate test does NOT need to populate
// the scanners.
type nightlyTickDeps struct {
	TMDBHolder       *adapters.TMDBClientHolder
	SeriesStaleScan  SeriesStaleScanner
	PeopleStaleScan  PeopleStaleScanner
	EnrichmentErrors appenrich.EnrichmentErrorRepo
	Dispatcher       *appenrich.DispatcherImpl
	Log              *slog.Logger
}

// runNightlyTick is the nightly stale-and-retry sweep body. Invoked by
// the boot scheduler at 04:00 UTC per bootstrap.go cron registration.
//
// 483 (B-23): early-return when the TMDB holder is empty. Symmetric with
// the cold-start `ShouldSweep` gate at enrichment.go:683 and the
// canon-images recovery gate at :646. Without the gate, an unconfigured
// install (operator has not saved a TMDB key) emits ~600 enrichment_errors
// rows per tick via handleTMDBError's retryable-backoff write, costing
// log noise, DB bloat, and counter inflation. The gate is one
// atomic.Load — negligible cost.
//
// DEBUG (not INFO): on a chronically-unconfigured install the gate
// fires nightly forever; INFO would spam the daily log window. Same
// level used by the OMDb activation skip log at enrichment.go:543-547.
func runNightlyTick(ctx context.Context, d nightlyTickDeps) {
	if d.TMDBHolder.Load() == nil {
		d.Log.DebugContext(ctx, "enrichment.nightly.skipped",
			slog.String("reason", "tmdb_holder_empty"))
		return
	}

	now := time.Now().UTC()
	// Series sweep: cutoff is 2 × continuing-series TTL (24h).
	// Stale-rows are series whose canon enrichment_tmdb_synced_at
	// is NULL or older than the cutoff; retry-rows are
	// enrichment_errors entries with next_attempt_at <= now.
	seriesStaleTTL := 2 * 24 * time.Hour
	seriesStale, err := d.SeriesStaleScan.ListStaleForTMDB(ctx, seriesStaleTTL, 100)
	if err != nil {
		d.Log.WarnContext(ctx, "enrichment.nightly.stale_scan_failed",
			slog.String("source", string(enrichment.SourceTMDBSeries)),
			slog.String("error", err.Error()))
	}
	seriesRetries, err := d.EnrichmentErrors.ListDueForRetry(ctx, enrichment.SourceTMDBSeries, now, 100)
	if err != nil {
		d.Log.WarnContext(ctx, "enrichment.nightly.retry_due_failed",
			slog.String("source", string(enrichment.SourceTMDBSeries)),
			slog.String("error", err.Error()))
	}
	for _, id := range seriesStale {
		d.Dispatcher.Enqueue(appenrich.EntitySeries, int64(id), appenrich.PriorityCold)
	}
	for _, e := range seriesRetries {
		d.Dispatcher.Enqueue(appenrich.EntitySeries, e.EntityID, appenrich.PriorityCold)
	}

	// 212: person sweep — 30d person TTL → cutoff = now - 60d.
	personStaleTTL := 60 * 24 * time.Hour
	var personStale []int64
	if d.PeopleStaleScan != nil {
		personStale, err = d.PeopleStaleScan.ListStaleForTMDB(ctx, personStaleTTL, 200)
		if err != nil {
			d.Log.WarnContext(ctx, "enrichment.nightly.stale_scan_failed",
				slog.String("source", string(enrichment.SourceTMDBPerson)),
				slog.String("error", err.Error()))
		}
	}
	personRetries, err := d.EnrichmentErrors.ListDueForRetry(ctx, enrichment.SourceTMDBPerson, now, 200)
	if err != nil {
		d.Log.WarnContext(ctx, "enrichment.nightly.retry_due_failed",
			slog.String("source", string(enrichment.SourceTMDBPerson)),
			slog.String("error", err.Error()))
	}
	for _, id := range personStale {
		d.Dispatcher.Enqueue(appenrich.EntityPerson, id, appenrich.PriorityCold)
	}
	for _, e := range personRetries {
		d.Dispatcher.Enqueue(appenrich.EntityPerson, e.EntityID, appenrich.PriorityCold)
	}

	d.Log.InfoContext(ctx, "enrichment.nightly.swept",
		slog.Int("series_stale", len(seriesStale)),
		slog.Int("series_retries", len(seriesRetries)),
		slog.Int("person_stale", len(personStale)),
		slog.Int("person_retries", len(personRetries)),
	)
}

// dispatcherHolder is a late-binding holder satisfying
// appenrich.Dispatcher. It exists to break the construction cycle
// between series_worker (needs Dispatcher) and dispatcher (needs
// series worker's Handle). The holder is constructed empty, handed
// to series_worker.deps, and the real dispatcher is plugged in via
// set() AFTER NewDispatcher returns. Concurrency: set() runs
// before dispatcher.Start, so the inner pointer is established
// before any reader goroutine exists.
type dispatcherHolder struct {
	inner appenrich.Dispatcher
}

func (h *dispatcherHolder) set(d appenrich.Dispatcher) { h.inner = d }

func (h *dispatcherHolder) Enqueue(kind appenrich.EntityKind, id int64, p appenrich.Priority) {
	if h.inner == nil {
		return
	}
	h.inner.Enqueue(kind, id, p)
}

func (h *dispatcherHolder) Close() {
	if h.inner == nil {
		return
	}
	h.inner.Close()
}

// EnrichmentRepoBundle is the dependency bundle main.go fills in.
// Kept as an explicit struct so the BuildEnrichment signature stays
// scannable.
type EnrichmentRepoBundle struct {
	Series       appenrich.SeriesRepo
	SeriesTexts  appenrich.SeriesTextsRepo
	Seasons      appenrich.SeasonsRepo
	Episodes     appenrich.EpisodesRepo
	EpisodeTexts appenrich.EpisodeTextsRepo
	// SeasonTexts — B3b (Story 581): season-localization write port
	// consumed by SeriesWorker.RefreshSeasonSlim. Production impl is
	// *enrichpersistence.SeasonTextsRepository (B3a). Nil-OK — when nil
	// the worker skips the season_texts step (episodes/texts still write).
	SeasonTexts appenrich.SeasonTextsRepo
	// SeriesMediaTexts — C-posters-A (Story 584a): per-language poster
	// write port. Nil-OK. *enrichpersistence.SeriesMediaTextsRepository.
	SeriesMediaTexts appenrich.SeriesMediaTextsRepo
	// SeasonMediaTexts — S-C2: per-language SEASON poster write port. Nil-OK.
	// *enrichpersistence.SeasonMediaTextsRepository.
	SeasonMediaTexts appenrich.SeasonMediaTextsRepo
	People           peopleRepoCombined
	Genres           appenrich.GenresRepo
	Keywords         appenrich.KeywordsRepo
	Networks         appenrich.NetworksRepo
	Companies        appenrich.CompaniesRepo
	Videos           appenrich.VideosRepoPort
	ContentRatings   appenrich.ContentRatingsRepoPort
	ExternalIDs      appenrich.ExternalIDsRepoPort
	Recommendations  appenrich.RecommendationsRepoPort
	// SeriesRecCanon — Story 571 B-54: narrow rec-media overwrite port
	// consumed by A3b RefreshRecommendations. Production impl is the same
	// *SeriesRepository that satisfies Series above (Go duck-typing on
	// UpdateRecCanonMedia). Nil-OK — degrades A3b to pre-571 behavior
	// (rec children stay locked to en-US poster on cold visit).
	SeriesRecCanon appenrich.SeriesRecCanonWriter
	// EnrichmentErrors — D-3 failure write surface (RecordFailure /
	// ClearOnSuccess / ListDueForRetry / GetForEntity /
	// GetByEntitySource). Used by all three workers + the composer's
	// freshness adapter.
	EnrichmentErrors appenrich.EnrichmentErrorRepo
	// 212 additions:
	PersonBiographies appenrich.PersonBiographiesPort
	PersonCredits     appenrich.PersonCreditsPort
	// PersonCreditsTexts — S-G: per-language cast character-name write
	// port consumed by SeriesWorker.RefreshCast. Nil-OK.
	// *enrichpersistence.PersonCreditsTextsRepository.
	PersonCreditsTexts appenrich.PersonCreditsTextsPort
	ColdStartScanner   appenrich.ColdStartScanner
	// SeriesStaleScan — D-3 (464b): nightly TMDB-stale series scan.
	// Production impl wraps *SeriesRepository.ListStaleForTMDB. Required
	// when enrichment is wired (workers + composer need it).
	SeriesStaleScan SeriesStaleScanner
	// PeopleStaleScan — D-3 (464b): nightly TMDB-stale people scan.
	// Production impl wraps *PeopleRepository.ListStaleForTMDB. Nil-OK
	// — when nil the dispatcher loop skips the person staleness sweep
	// (retry-due rows still fire).
	PeopleStaleScan PeopleStaleScanner
	// RefreshPicker — Story 534: tiered (hot/normal/cold) candidate
	// picker used by the background refresh scheduler. Production
	// impl wraps *SeriesRepository.PickRefreshCandidates via
	// NewRefreshPickerAdapter. Nil-OK — when nil the scheduler is
	// skipped at boot.
	RefreshPicker appenrich.RefreshPicker
	// 213: ListLibraryWithIMDBStale source for the OMDb daily batch.
	// Production impl wraps *SeriesRepository. Nil-OK — when nil the
	// OMDb daily-batch closure logs and short-circuits.
	LibraryWithIMDB OMDbBatchScanner
	// 214 (F-1): media assets read/write. Constructed in main.go
	// outside BuildEnrichment so the same handle is shared with the
	// HTTP MediaHandler. Nil-OK — when nil the pre-warm pipeline
	// stays off (downloader needs a repo to write rows).
	MediaAssets appmedia.AssetRepo
	// 214 (F-1): blob store handle constructed in main.go. Nil-OK —
	// when nil the pre-warm pipeline stays off.
	MediaStore mediastore.Store
}

// SeriesStaleScanner is the application-layer surface for the nightly
// "series whose TMDB enrichment is stale" query. Production impl wraps
// *SeriesRepository.ListStaleForTMDB.
type SeriesStaleScanner interface {
	ListStaleForTMDB(ctx context.Context, ttl time.Duration, limit int) ([]domain.SeriesID, error)
}

// PeopleStaleScanner is the application-layer surface for the nightly
// "person whose TMDB enrichment is stale" query. Production impl wraps
// *PeopleRepository.ListStaleForTMDB.
type PeopleStaleScanner interface {
	ListStaleForTMDB(ctx context.Context, ttl time.Duration, limit int) ([]int64, error)
}

// OMDbBatchScanner is the application-layer surface for the
// "library series with imdb_id whose OMDb sync is stale" query.
// Production impl wraps *SeriesRepository. Staleness is age-based
// (W18-5) and computed inside the query — no ttl param.
type OMDbBatchScanner interface {
	ListLibraryWithIMDBStale(ctx context.Context, limit int) ([]int64, error)
}

// omdbBatchScannerAdapter wraps *SeriesRepository to satisfy
// OMDbBatchScanner. Out-of-application boundary, no
// imports of infrastructure/database from app.
type omdbBatchScannerAdapter struct {
	inner *enrichpersistence.SeriesRepository
}

// NewOMDbBatchScannerAdapter returns the wrapper for main.go's wiring.
func NewOMDbBatchScannerAdapter(s *enrichpersistence.SeriesRepository) OMDbBatchScanner {
	return omdbBatchScannerAdapter{inner: s}
}

func (a omdbBatchScannerAdapter) ListLibraryWithIMDBStale(ctx context.Context, limit int) ([]int64, error) {
	ids, err := a.inner.ListLibraryWithIMDBStale(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(ids))
	for i, id := range ids {
		out[i] = int64(id)
	}
	return out, nil
}

// peopleRepoCombined is the intersection interface main.go's
// *PeopleRepository must satisfy. The series worker uses PeopleRepo
// (GetByTMDBID + Upsert); the person worker uses PeopleWritePort
// (Get + Upsert). One concrete repo implements both shapes — Go's
// structural typing handles the rest.
type peopleRepoCombined interface {
	appenrich.PeopleRepo
	appenrich.PeopleWritePort
}

// ---- repo → port adapters ------------------------------------------

// VideosRepoAdapter wraps *enrichpersistence.VideosRepository to satisfy
// VideosRepoPort. The worker's VideoRow uses plain strings; the
// underlying VideoModel persists optional fields as *string —
// translate at this boundary.
type VideosRepoAdapter struct {
	Inner *enrichpersistence.VideosRepository
}

func (a VideosRepoAdapter) Upsert(ctx context.Context, v appenrich.VideoRow) error {
	m := enrichpersistence.Video{
		SeriesID:    v.SeriesID,
		Name:        v.Name,
		Official:    v.Official,
		PublishedAt: v.PublishedAt,
	}
	if v.TMDBID != "" {
		id := v.TMDBID
		m.TMDBVideoID = &id
	}
	if v.Site != "" {
		s := v.Site
		m.Site = &s
	}
	if v.Key != "" {
		k := v.Key
		m.Key = &k
	}
	if v.Type != "" {
		t := v.Type
		m.Type = &t
	}
	if v.Language != "" {
		l := v.Language
		m.Language = &l
	}
	if m.Name == "" {
		// VideosRepository.Upsert requires a non-empty name. Skip
		// silently — a video with no name has nothing to display.
		return nil
	}
	_, err := a.Inner.Upsert(ctx, m)
	return err
}

// ContentRatingsRepoAdapter wraps the canonical repo to match the
// (seriesID, country, rating) tuple shape the worker uses.
type ContentRatingsRepoAdapter struct {
	Inner *enrichpersistence.ContentRatingsRepository
}

func (a ContentRatingsRepoAdapter) Upsert(ctx context.Context, seriesID domain.SeriesID, country, rating string) error {
	if country == "" || rating == "" {
		return nil
	}
	return a.Inner.Upsert(ctx, enrichpersistence.ContentRating{
		SeriesID: seriesID, CountryCode: country, Rating: rating,
	})
}

// GenresRepoAdapter composes GenresRepository + GenresI18nRepository
// behind the appenrich.GenresRepo port. The port treats the i18n
// write as a single method; the production split is invisible to
// the worker.
type GenresRepoAdapter struct {
	Main *enrichpersistence.GenresRepository
	I18n *enrichpersistence.GenresI18nRepository
}

func (a GenresRepoAdapter) Upsert(ctx context.Context, g taxonomy.Genre) (int64, error) {
	return a.Main.Upsert(ctx, g)
}

func (a GenresRepoAdapter) UpsertI18n(ctx context.Context, genreID int64, language, name string) error {
	return a.I18n.Upsert(ctx, taxonomy.GenreI18n{
		GenreID:  genreID,
		Language: language,
		Name:     name,
	})
}

func (a GenresRepoAdapter) Set(ctx context.Context, seriesID domain.SeriesID, ids []int64) error {
	return a.Main.Set(ctx, seriesID, ids)
}

// KeywordsRepoAdapter mirrors GenresRepoAdapter.
type KeywordsRepoAdapter struct {
	Main *enrichpersistence.KeywordsRepository
	I18n *enrichpersistence.KeywordsI18nRepository
}

func (a KeywordsRepoAdapter) Upsert(ctx context.Context, k taxonomy.Keyword) (int64, error) {
	return a.Main.Upsert(ctx, k)
}

func (a KeywordsRepoAdapter) UpsertI18n(ctx context.Context, keywordID int64, language, name string) error {
	return a.I18n.Upsert(ctx, taxonomy.KeywordI18n{
		KeywordID: keywordID,
		Language:  language,
		Name:      name,
	})
}

func (a KeywordsRepoAdapter) Set(ctx context.Context, seriesID domain.SeriesID, ids []int64) error {
	return a.Main.Set(ctx, seriesID, ids)
}

// ExternalIDsRepoAdapter wraps the canonical repo to match the
// (entityType, entityID, provider, value) tuple shape.
type ExternalIDsRepoAdapter struct {
	Inner *enrichpersistence.ExternalIDsRepository
}

func (a ExternalIDsRepoAdapter) Upsert(ctx context.Context, entityType enrichment.EntityType, entityID int64, provider, value string) error {
	if provider == "" || value == "" {
		return nil
	}
	return a.Inner.Upsert(ctx, entityType, entityID, provider, value)
}

// ---- 212 adapters --------------------------------------------------

// PersonCreditsRepoAdapter translates the domain-level
// []people.PersonCredit shape the person worker emits into the
// repository's []database.PersonCreditModel write rows. The domain
// type carries pointer-typed nullable fields (ReleaseDate *time.Time,
// TMDBRating *float64, etc.); the model carries year *int + poster_path
// *string. The conversion lives here so the application layer never
// touches the database package.
type PersonCreditsRepoAdapter struct {
	Inner *enrichpersistence.PersonCreditsRepository
}

func (a PersonCreditsRepoAdapter) BatchUpsert(ctx context.Context, credits []people.PersonCredit) ([]int64, error) {
	if len(credits) == 0 {
		return nil, nil
	}
	rows := make([]database.PersonCreditModel, 0, len(credits))
	for _, c := range credits {
		rows = append(rows, database.PersonCreditModel{
			PersonID:      c.PersonID,
			TMDBCreditID:  c.TMDBCreditID,
			MediaType:     c.MediaType,
			TMDBMediaID:   int(c.TMDBMediaID),
			Title:         c.Title,
			OriginalTitle: c.OriginalTitle,
			Year:          yearFromReleaseDate(c.ReleaseDate),
			CharacterName: c.CharacterName,
			Kind:          string(c.Kind),
			Department:    c.Department,
			Job:           c.Job,
			PosterPath:    c.PosterAsset,
			VoteAverage:   c.TMDBRating,
			TMDBVotes:     c.TMDBVotes,
			EpisodeCount:  c.EpisodeCount,
		})
	}
	return a.Inner.BatchUpsert(ctx, rows)
}

// yearFromReleaseDate extracts the calendar year from a TMDB release
// date pointer. Used to populate person_credits.year (legacy column
// kept as a denormalised filter index for the H-1 list ordering).
func yearFromReleaseDate(t *time.Time) *int {
	if t == nil {
		return nil
	}
	y := t.Year()
	return &y
}

// coldStartScannerAdapter wraps *SeriesRepository to satisfy
// appenrich.ColdStartScanner. The adapter exists so the application
// port doesn't import infrastructure/database.
type coldStartScannerAdapter struct {
	inner *enrichpersistence.SeriesRepository
}

// NewColdStartScannerAdapter returns the wrapper. Kept exported for
// main.go's wiring.
func NewColdStartScannerAdapter(s *enrichpersistence.SeriesRepository) appenrich.ColdStartScanner {
	return coldStartScannerAdapter{inner: s}
}

// ListMissingTMDBSync — D-3 column-on-canon query for the cold-start
// backfill loop. Forwards to the underlying repository.
func (a coldStartScannerAdapter) ListMissingTMDBSync(ctx context.Context, limit int) ([]domain.SeriesID, error) {
	return a.inner.ListMissingTMDBSync(ctx, limit)
}

// mediaPrewarmerAdapter satisfies appenrich.MediaPrewarmer against
// the application/media Enqueuer. The two request types are mirrors
// (UpstreamURL / Kind / Extension); the adapter exists because the
// enrichment package may NOT import application/media (sibling-app
// layer rule).
type mediaPrewarmerAdapter struct {
	eq *appmedia.Enqueuer
}

func (a mediaPrewarmerAdapter) Enqueue(ctx context.Context, reqs []appenrich.MediaPrewarmRequest) {
	if a.eq == nil || len(reqs) == 0 {
		return
	}
	out := make([]appmedia.EnqueueRequest, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, appmedia.EnqueueRequest{
			UpstreamURL: r.UpstreamURL,
			Kind:        r.Kind,
			Extension:   r.Extension,
		})
	}
	a.eq.Enqueue(ctx, out)
}

// seriesStaleScanAdapter wraps *SeriesRepository to satisfy
// SeriesStaleScanner. Out-of-application boundary; no application
// imports of infrastructure/database.
type seriesStaleScanAdapter struct {
	inner *enrichpersistence.SeriesRepository
}

// NewSeriesStaleScanAdapter returns the wrapper for main.go's wiring.
func NewSeriesStaleScanAdapter(s *enrichpersistence.SeriesRepository) SeriesStaleScanner {
	return seriesStaleScanAdapter{inner: s}
}

func (a seriesStaleScanAdapter) ListStaleForTMDB(ctx context.Context, ttl time.Duration, limit int) ([]domain.SeriesID, error) {
	return a.inner.ListStaleForTMDB(ctx, ttl, limit)
}

// peopleStaleScanAdapter wraps *PeopleRepository to satisfy
// PeopleStaleScanner.
type peopleStaleScanAdapter struct {
	inner *enrichpersistence.PeopleRepository
}

// NewPeopleStaleScanAdapter returns the wrapper for main.go's wiring.
func NewPeopleStaleScanAdapter(p *enrichpersistence.PeopleRepository) PeopleStaleScanner {
	return peopleStaleScanAdapter{inner: p}
}

func (a peopleStaleScanAdapter) ListStaleForTMDB(ctx context.Context, ttl time.Duration, limit int) ([]int64, error) {
	return a.inner.ListStaleForTMDB(ctx, ttl, limit)
}

// refreshPickerAdapter wraps *SeriesRepository.PickRefreshCandidates
// to satisfy the appenrich.RefreshPicker port. Story 534. Out-of-
// application boundary; the adapter maps persistence-flavoured
// RefreshCandidate (domain.SeriesID) to the app-port shape (int64).
type refreshPickerAdapter struct {
	inner *enrichpersistence.SeriesRepository
}

// NewRefreshPickerAdapter returns the wrapper for main.go's wiring.
func NewRefreshPickerAdapter(s *enrichpersistence.SeriesRepository) appenrich.RefreshPicker {
	return refreshPickerAdapter{inner: s}
}

func (a refreshPickerAdapter) PickRefreshCandidates(
	ctx context.Context,
	now time.Time,
	ttl enrichment.RefreshTTL,
	limit int,
) ([]appenrich.RefreshCandidate, error) {
	rows, err := a.inner.PickRefreshCandidates(ctx, now, ttl, limit)
	if err != nil {
		return nil, err
	}
	out := make([]appenrich.RefreshCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, appenrich.RefreshCandidate{
			SeriesID:      int64(r.SeriesID),
			Tier:          r.Tier,
			MissingPoster: r.MissingPoster,
		})
	}
	return out, nil
}

// seriesWorkerForceAdapter bridges the int64 application-port shape
// (appenrich.SeriesForceRefresher) to *SeriesWorker.HandleForced
// which takes domain.SeriesID. Mirrors the existing
// seriesStaleScanAdapter pattern.
type seriesWorkerForceAdapter struct {
	inner *appenrich.SeriesWorker
}

func (a seriesWorkerForceAdapter) HandleForced(ctx context.Context, id int64) error {
	return a.inner.HandleForced(ctx, domain.SeriesID(id))
}
