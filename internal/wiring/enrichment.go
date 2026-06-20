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
	// rows that lack a sync_log(tmdb_series) entry are enqueued at
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
	// settings change (UI Upsert) rebuilds the client in-place. nil
	// when the corresponding subsystem was unavailable at boot:
	//   - TMDBHolder nil  → TMDB disabled at boot (no workers).
	//   - OMDbHolder nil  → never (always allocated; may be empty).
	TMDBHolder     *adapters.TMDBClientHolder
	OMDbHolder     *adapters.OMDbClientHolder
	TMDBFactoryCfg adapters.TMDBClientFactoryConfig
}

// BuildEnrichment builds the dispatcher + nightly stale scan closure.
// Returns a nil dispatcher when TMDB is disabled or no token is set
// (boot path stays green on a freshly-installed instance with no
// runtime config yet).
func BuildEnrichment(
	rootCtx context.Context,
	extSub *adapters.ExternalServicesSubscriber,
	bootstrap *config.Bootstrap,
	repos EnrichmentRepoBundle,
	tx appenrich.Transactor,
	quotaCounter quota.QuotaCounter,
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
	// All three wraps fire BEFORE the disabled early-return below so
	// the "enrichment.disabled" record also carries domain="enrichment".
	enrichmentLog := sharedports.DomainLogger(log, "enrichment")
	omdbLog := sharedports.DomainLogger(log, "omdb")
	tmdbLog := sharedports.DomainLogger(log, "tmdb")

	settings := extSub.Get(infraextsvc.ServiceTMDB)
	if !settings.Enabled || settings.APIKey == "" {
		enrichmentLog.InfoContext(rootCtx, "enrichment.disabled",
			slog.Bool("enabled", settings.Enabled),
			slog.Bool("api_key", settings.APIKey != ""))
		// Story 352 — return an empty bundle with NO holders. A from-
		// disabled→enabled flip at runtime requires a process restart
		// because the workers / dispatcher don't exist yet, and
		// constructing them post-boot would tangle with bus / scheduler
		// ordering. The operator-facing UI logs this explicitly when the
		// reload subscriber declines a same-restart enable.
		return &EnrichmentBundle{}, nil
	}

	httpClient, err := infraextsvc.HttpClientFor(settings)
	if err != nil {
		return nil, err
	}

	// Story 312: surface the configured TMDB external-services proxy at
	// boot so the operator can confirm image.tmdb.org goes through the
	// same proxy as api.themoviedb.org (RU DPI blocks both the same way).
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
	// httpx.NewMetricsTransport("tmdb", ...). That clone captures the
	// CURRENT httpClient.Transport (the raw proxy transport).
	//
	// AFTER tmdb.New returns we mutate the SHARED httpClient pointer in
	// place — wrapping its Transport with httpx.NewMetricsTransport
	// ("tmdb_cdn", ...) — so every subsequent http.Request issued via
	// the shared pointer (i.e. every image.tmdb.org fetch from the
	// media downloader / on-demand fetcher) flows through the
	// "tmdb_cdn" metric writes.
	//
	// This ordering guarantees api.themoviedb.org metrics carry ONLY
	// client="tmdb" and image.tmdb.org metrics carry ONLY
	// client="tmdb_cdn" — no double-write. Canary check: a
	// client="tmdb_cdn" row with endpoint matching a /tv/... or /search/...
	// path means the order is broken.
	//
	// Story 352 — the factory below mirrors this boot path verbatim so
	// the reload subscriber can rebuild a metric-wrapped TMDB API client
	// on key/proxy change. The downloader's "tmdb_cdn" wrap is NOT
	// rebuilt by the subscriber because the downloader was constructed
	// with the SHARED httpClient pointer below — Story 352 is scoped to
	// the api.themoviedb.org client only.
	tmdbFactoryCfg := adapters.TMDBClientFactoryConfig{
		Language: tmdb.DefaultLanguage,
		RPS:      bootstrap.ExternalServices.TMDBAPIRPS,
		Logger:   tmdbLog,
	}
	tmdbClient, err := tmdb.New(tmdb.Config{
		Token:      settings.APIKey,
		HTTPClient: httpClient,
		Language:   tmdbFactoryCfg.Language,
		RPS:        tmdbFactoryCfg.RPS,
		Logger:     tmdbFactoryCfg.Logger,
	})
	if err != nil {
		return nil, err
	}
	tmdbHolder := adapters.NewTMDBClientHolder()
	tmdbHolder.Set(tmdbClient)
	// Story 351 — see comment block above. tmdb.New has captured the
	// pre-wrap Transport into its clone; now we wrap the SHARED pointer
	// for the downloader's use.
	httpClient.Transport = httpx.NewMetricsTransport("tmdb_cdn", httpx.TMDBCDNEndpointFor, httpClient.Transport)

	// 214 (F-1): media pre-warm pipeline. Only constructed when both
	// the blob store + the media_assets repo are available; the pair
	// is required for the downloader to make persistent progress.
	// httpClient is SHARED with tmdbClient above so the same
	// proxy-connection pool serves both API + image fetches (RU DPI
	// blocks image.tmdb.org the same way it blocks api.themoviedb.org).
	var (
		mediaEnqueuer   *appmedia.Enqueuer
		mediaDownloader *appmedia.Downloader
		mediaOnDemand   appmedia.OnDemandFetcher
		mediaPrewarmer  appenrich.MediaPrewarmer // nil OK
	)
	if repos.MediaAssets != nil && repos.MediaStore != nil {
		mediaEnqueuer = appmedia.NewEnqueuer(enrichmentLog)
		// Story 346: split CDN limiter from the TMDB API limiter.
		// bootstrap.ExternalServices.TMDBCDNRPS=0 → downloader default
		// (100 rps for image.tmdb.org); override via
		// SEASONFILL_TMDB_CDN_RPS.
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
		// async paths. The wiring layer in main.go calls
		// seriesDetailMediaResolver.SetSideEffects(mediaEnqueuer,
		// mediaOnDemand) once the bundle returns.
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
	worker, err := appenrich.NewSeriesWorker(appenrich.SeriesWorkerDeps{
		TMDB:            tmdbHolder,
		Tx:              tx,
		Language:        tmdb.DefaultLanguage,
		Series:          repos.Series,
		SeriesTexts:     repos.SeriesTexts,
		Seasons:         repos.Seasons,
		Episodes:        repos.Episodes,
		EpisodeTexts:    repos.EpisodeTexts,
		People:          repos.People,
		SeriesPeople:    repos.SeriesPeople,
		Genres:          repos.Genres,
		Keywords:        repos.Keywords,
		Networks:        repos.Networks,
		Companies:       repos.Companies,
		Videos:          repos.Videos,
		ContentRatings:  repos.ContentRatings,
		ExternalIDs:     repos.ExternalIDs,
		Recommendations: repos.Recommendations,
		SyncLog:         repos.SyncLog,
		MediaPrewarmer:  mediaPrewarmer, // 214 (F-1): nil-OK when MediaStore/MediaAssets absent
		Dispatcher:      holder,
		Logger:          enrichmentLog,
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
		SyncLog:           repos.SyncLog,
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
		Client:  omdbHolder.Get,
		Budget:  omdbBudget,
		Tx:      tx,
		Series:  repos.Series,
		SyncLog: repos.SyncLog,
		Logger:  omdbLog,
	})
	if err != nil {
		return nil, fmt.Errorf("new omdb worker: %w", err)
	}
	omdbWorkerHandle := func(ctx context.Context, id int64) error {
		return omdbWorker.Handle(ctx, domain.SeriesID(id))
	}
	var (
		omdbDailyBatch  func(context.Context)
		omdbBudgetReset func(context.Context)
	)
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
			slog.Bool("api_key", omdbSettings.APIKey != ""))
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

	// 213: Daily-batch + budget-reset closures (cron 04:30 / 04:00).
	// Constructed AFTER dispatcher exists so the batch closure can
	// reference dispatcher.Enqueue. Cron registration is gated by
	// boot-time OMDb enablement — runtime enable lifts the worker but
	// the cron stays off until the next process restart (the scheduler
	// itself is reload-aware via a separate subscriber; new jobs are
	// not registered post-boot).
	if omdbEnabledAtBoot {
		omdbDailyBatch = func(ctx context.Context) {
			if repos.LibraryWithIMDB == nil {
				omdbLog.WarnContext(ctx, "enrichment.omdb.daily_batch.no_scanner")
				return
			}
			const batchLimit = 900
			ttl := enrichment.TTL(enrichment.SourceOMDb, enrichment.KindOMDb)
			ids, err := repos.LibraryWithIMDB.ListLibraryWithIMDBStale(ctx, ttl, batchLimit)
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
		omdbBudgetReset = func(ctx context.Context) {
			before := omdbBudget.Remaining()
			omdbBudget.Reset()
			omdbLog.InfoContext(ctx, "enrichment.omdb.budget.reset",
				slog.Int("before", before),
				slog.Int("after", omdbBudget.Remaining()))
		}
	}

	nightly := func(ctx context.Context) {
		now := time.Now().UTC()
		// Series sweep: cutoff is now - 2 × continuing-series TTL
		// (24h). Ended-series rows with 30d TTL are still selected
		// since they're already older.
		seriesCutoff := now.Add(-2 * 24 * time.Hour)
		seriesStale, err := repos.SyncLog.StaleScan(ctx, enrichment.SourceTMDBSeries, seriesCutoff, 100)
		if err != nil {
			enrichmentLog.WarnContext(ctx, "enrichment.nightly.stale_scan_failed",
				slog.String("source", string(enrichment.SourceTMDBSeries)),
				slog.String("error", err.Error()))
		}
		seriesRetries, err := repos.SyncLog.RetryDue(ctx, enrichment.SourceTMDBSeries, now, 100)
		if err != nil {
			enrichmentLog.WarnContext(ctx, "enrichment.nightly.retry_due_failed",
				slog.String("source", string(enrichment.SourceTMDBSeries)),
				slog.String("error", err.Error()))
		}
		for _, e := range seriesStale {
			dispatcher.Enqueue(appenrich.EntitySeries, e.EntityID, appenrich.PriorityCold)
		}
		for _, e := range seriesRetries {
			dispatcher.Enqueue(appenrich.EntitySeries, e.EntityID, appenrich.PriorityCold)
		}

		// 212: person sweep — 30d person TTL → cutoff = now - 60d.
		personCutoff := now.Add(-60 * 24 * time.Hour)
		personStale, err := repos.SyncLog.StaleScan(ctx, enrichment.SourceTMDBPerson, personCutoff, 200)
		if err != nil {
			enrichmentLog.WarnContext(ctx, "enrichment.nightly.stale_scan_failed",
				slog.String("source", string(enrichment.SourceTMDBPerson)),
				slog.String("error", err.Error()))
		}
		personRetries, err := repos.SyncLog.RetryDue(ctx, enrichment.SourceTMDBPerson, now, 200)
		if err != nil {
			enrichmentLog.WarnContext(ctx, "enrichment.nightly.retry_due_failed",
				slog.String("source", string(enrichment.SourceTMDBPerson)),
				slog.String("error", err.Error()))
		}
		for _, e := range personStale {
			dispatcher.Enqueue(appenrich.EntityPerson, e.EntityID, appenrich.PriorityCold)
		}
		for _, e := range personRetries {
			dispatcher.Enqueue(appenrich.EntityPerson, e.EntityID, appenrich.PriorityCold)
		}

		enrichmentLog.InfoContext(ctx, "enrichment.nightly.swept",
			slog.Int("series_stale", len(seriesStale)),
			slog.Int("series_retries", len(seriesRetries)),
			slog.Int("person_stale", len(personStale)),
			slog.Int("person_retries", len(personRetries)),
		)
	}

	// 212 + 318: cold-start backfill closure. Hands repos.ColdStartScanner
	// + dispatcher to the application-layer loop; safe to call from a
	// background goroutine in main.go AFTER dispatcher.Start. The loop
	// re-sweeps every Enrichment.ColdStartResweepInterval (default 60s)
	// so series the dispatcher dropped on a saturated cold channel are
	// re-enqueued on the next tick. RunBackfillLoop returns when ctx
	// is Done.
	resweepInterval := bootstrap.Enrichment.ColdStartResweepInterval
	coldStart := func(ctx context.Context) {
		if repos.ColdStartScanner == nil {
			return
		}
		// Story 319 — one-shot recovery sweep for canon rows whose
		// poster_asset / backdrop_asset were nulled by the legacy
		// recommendation-stub upsert bug. Enqueues each at
		// PriorityCold so the TMDB sync repopulates the paths via
		// MergeSeries. Safe to re-run (idempotent — healed rows have
		// both columns non-NULL and are not returned). Set
		// SEASONFILL_ENRICHMENT_CANON_RECOVERY_DISABLED=1 to skip
		// during disaster recovery / manual control.
		if os.Getenv("SEASONFILL_ENRICHMENT_CANON_RECOVERY_DISABLED") != "1" {
			// Story 346: per-kind breakdown log so operators can
			// confirm the sweep observed both poster + backdrop NULLs
			// (or just one). Counted BEFORE the enqueue so a converging
			// counter reads "this many rows still need fixing" rather
			// than "this many we enqueued". Cheap (two indexed
			// COUNT(*) on hydration='full'); failures non-fatal.
			posterNull, backdropNull, cntErr := repos.ColdStartScanner.CountCanonImagesBreakdown(ctx)
			if cntErr != nil {
				enrichmentLog.WarnContext(ctx, "enrichment.canon_images.recovery.breakdown_failed",
					slog.String("error", cntErr.Error()))
			} else {
				enrichmentLog.InfoContext(ctx, "enrichment.canon_images.recovery.breakdown",
					slog.Int("poster_null", posterNull),
					slog.Int("backdrop_null", backdropNull))
				observability.AddRecoverySweepEnqueued("poster", posterNull)
				observability.AddRecoverySweepEnqueued("backdrop", backdropNull)
			}
			ids, err := repos.ColdStartScanner.ListCanonImagesCorrupted(ctx, 5000)
			if err != nil {
				enrichmentLog.WarnContext(ctx, "enrichment.canon_images.recovery.failed",
					slog.String("error", err.Error()))
			} else if len(ids) > 0 {
				for _, id := range ids {
					dispatcher.Enqueue(appenrich.EntitySeries, int64(id), appenrich.PriorityCold)
				}
				enrichmentLog.InfoContext(ctx, "enrichment.canon_images.recovery.enqueued",
					slog.Int("series_count", len(ids)),
					slog.String("priority", "cold"))
			}
		}
		appenrich.RunBackfillLoop(ctx, repos.ColdStartScanner, dispatcher, resweepInterval, enrichmentLog)
	}

	dispatcher.Start(rootCtx)
	// 214 (F-1): start the media downloader workers AFTER the
	// dispatcher is up so the first pre-warm enqueue always lands
	// on a live consumer. Close is driven from main.go's shutdown
	// path (Enqueuer.Close → Downloader.Close).
	if mediaDownloader != nil {
		mediaDownloader.Start(rootCtx)
	}
	return &EnrichmentBundle{
		Dispatcher:       dispatcher,
		Nightly:          nightly,
		ColdStart:        coldStart,
		OMDbDailyBatch:   omdbDailyBatch,
		OMDbBudgetReset:  omdbBudgetReset,
		MediaEnqueuer:    mediaEnqueuer,
		MediaDownloader:  mediaDownloader,
		MediaOnDemand:    mediaOnDemand,
		MediaHTTP:        httpClient,
		UsesQuotaCounter: quotaCounter != nil && omdbEnabledAtBoot,
		TMDBHolder:       tmdbHolder,
		OMDbHolder:       omdbHolder,
		TMDBFactoryCfg:   tmdbFactoryCfg,
	}, nil
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
	Series          appenrich.SeriesRepo
	SeriesTexts     appenrich.SeriesTextsRepo
	Seasons         appenrich.SeasonsRepo
	Episodes        appenrich.EpisodesRepo
	EpisodeTexts    appenrich.EpisodeTextsRepo
	People          peopleRepoCombined
	SeriesPeople    appenrich.SeriesPeopleRepo
	Genres          appenrich.GenresRepo
	Keywords        appenrich.KeywordsRepo
	Networks        appenrich.NetworksRepo
	Companies       appenrich.CompaniesRepo
	Videos          appenrich.VideosRepoPort
	ContentRatings  appenrich.ContentRatingsRepoPort
	ExternalIDs     appenrich.ExternalIDsRepoPort
	Recommendations appenrich.RecommendationsRepoPort
	SyncLog         appenrich.SyncLogRepo
	// 212 additions:
	PersonBiographies appenrich.PersonBiographiesPort
	PersonCredits     appenrich.PersonCreditsPort
	ColdStartScanner  appenrich.ColdStartScanner
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

// OMDbBatchScanner is the application-layer surface for the
// "library series with imdb_id whose OMDb sync is stale" query.
// Production impl wraps *SeriesRepository.
type OMDbBatchScanner interface {
	ListLibraryWithIMDBStale(ctx context.Context, ttl time.Duration, limit int) ([]int64, error)
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

func (a omdbBatchScannerAdapter) ListLibraryWithIMDBStale(ctx context.Context, ttl time.Duration, limit int) ([]int64, error) {
	ids, err := a.inner.ListLibraryWithIMDBStale(ctx, ttl, limit)
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

func (a coldStartScannerAdapter) ListMissingSyncLog(ctx context.Context, source string, limit int) ([]domain.SeriesID, error) {
	return a.inner.ListMissingSyncLog(ctx, source, limit)
}

// ListCanonImagesCorrupted — Story 319: forwards to the underlying
// repository. The wrapper exists so the application port doesn't
// import infrastructure/database.
func (a coldStartScannerAdapter) ListCanonImagesCorrupted(ctx context.Context, limit int) ([]domain.SeriesID, error) {
	return a.inner.ListCanonImagesCorrupted(ctx, limit)
}

// CountCanonImagesBreakdown — Story 346: forwards to the underlying
// repository.
func (a coldStartScannerAdapter) CountCanonImagesBreakdown(ctx context.Context) (int, int, error) {
	return a.inner.CountCanonImagesBreakdown(ctx)
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
