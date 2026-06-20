package wiring

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/torrentsync"
	webhookuc "github.com/alexmorbo/seasonfill/application/webhook"
	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	grab "github.com/alexmorbo/seasonfill/internal/grab/app"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
	watchdog "github.com/alexmorbo/seasonfill/internal/watchdog/infrastructure"
	infraregrab "github.com/alexmorbo/seasonfill/internal/watchdog/infrastructure/regrab"
	watchdogpersistence "github.com/alexmorbo/seasonfill/internal/watchdog/persistence"
	watchdogrest "github.com/alexmorbo/seasonfill/internal/watchdog/rest"
)

// WatchdogBundle holds the boot-time health monitor + state watchdog
// for per-instance Sonarr availability. Constructed once by
// BuildWatchdog; neither field is mutated after boot.
//
// Field-level invariants:
//
//   - Checker drives the periodic healthcheck loop (see server.go
//     lifecycle.Go("healthcheck")) and exposes the registry that the
//     scan UC (WithHealthRegistry) and the Watchdog rechecker share.
//
//   - Watchdog rechecks Unavailable* instances at per-state cadences
//     (D-2.3). It reads from Checker.Registry() and dispatches
//     rechecks back through Checker.
type WatchdogBundle struct {
	Checker  *healthcheck.Checker
	Watchdog *watchdog.Watchdog
}

// BuildWatchdog wires the healthcheck Checker and the state Watchdog
// against the persistence DB handle, the boot Sonarr client set, and
// the per-instance HealthCheck config map. No error path — every step
// is in-memory construction. The signature returns error to leave
// room for future seed-or-validate logic without a downstream
// signature churn.
func BuildWatchdog(persistence *PersistenceBundle, sonarr *SonarrBundle, log *slog.Logger) (*WatchdogBundle, error) {
	checker := healthcheck.New(persistence.DB, sonarr.SonarrClients)
	wd := watchdog.New(checker.Registry(), checker, log, sonarr.CfgByName)

	return &WatchdogBundle{
		Checker:  checker,
		Watchdog: wd,
	}, nil
}

// ScanBundle groups every component of the scan/grab/rescan stack
// constructed at boot. Returned by BuildScan; held on Server for the
// shutdown ladder (waitForScans reads ScanUC + ScanRepo) and for the
// reload subscriber fanout (SwapInstances, SwapDryRun on ScanUC).
//
// Field-level invariants:
//
//   - Evaluator is the per-instance decision evaluator. Shared by ScanUC
//     (per-cycle decisions) and the regrab use case (live in server.go).
//
//   - GrabUC is the grab pipeline (sonarr.Classifier + Transactor). It
//     fronts every grab dispatch — scheduled scans, manual rescans, and
//     the regrab orchestrator — so the same M-7 transactor wraps all
//     three success paths.
//
//   - ScanUC drives the periodic scan cycle. Its builder chain
//     (WithGrabUseCase, WithCooldowns, WithOrigins, WithSeriesCache,
//     WithHealthRegistry, WithWaitGroup) is preserved verbatim from
//     the pre-334 inline call — reordering or omitting any link
//     changes behaviour.
//
//   - RescanUC is the manual rescan path. Captures holder.Load so the
//     instance lookup is reload-aware.
//
//   - Sweeper is the cooldown-row sweep loop. Cadence is reload-aware
//     via SetInterval from the OnApplied fanout. Server.New spawns
//     Sweeper.Run on the lifecycle group.
//
//   - ScanRepo, GrabRepo, CooldownRepo, OriginRepo, DecisionRepo are
//     the per-instance repositories used by the scan stack. They are
//     also consumed by HTTP handlers (httpserver.NewServer), the
//     regrab use case, and Shutdown's waitForScans — so the bundle
//     re-exposes them rather than re-deriving in callers.
//
//   - Txr is the M-7 atomic-success-path transactor. Currently only
//     wired into GrabUC, but exposed on the bundle for downstream
//     stories (335 webhook, 337 regrab) that need transactional
//     boundaries against the same DB handle.
type ScanBundle struct {
	Evaluator    *evaluate.UseCase
	GrabUC       *grab.UseCase
	ScanUC       *scan.UseCase
	RescanUC     *rescan.UseCase
	Sweeper      *loops.SweepLoop
	ScanRepo     *repositories.ScanRepository
	GrabRepo     *grabpersistence.GrabRepository
	CooldownRepo *watchdogpersistence.CooldownRepository
	OriginRepo   *enrichpersistence.OriginReleaseRepository
	DecisionRepo *grabpersistence.DecisionRepository
	Txr          *repositories.GormTransactor
}

// BuildScan wires the scan + grab + rescan + cooldown-sweep stack.
//
// Construction order mirrors the pre-334 inline body verbatim:
//
//  1. Per-instance repos (scan, decision, grab, cooldown, origin).
//  2. Transactor.
//  3. Evaluator (decision history reader).
//  4. GrabUC + WithTransactor.
//  5. ScanUC builder chain: WithGrabUseCase → WithCooldowns →
//     WithOrigins → WithSeriesCache → WithHealthRegistry →
//     WithWaitGroup. Order preserved exactly.
//  6. RescanUC (captures sonarr.Holder.Load for reload-aware lookup).
//  7. Sweeper (cadence from cfg.Scan.CooldownSweep, default 15m).
//
// seriesCacheRepo is built locally here because the scan UC chain
// needs it; it is NOT re-exposed on the bundle (downstream consumers
// — seriesdetail, enrichment, webhook — construct their own instance
// from PersistenceBundle.DB since the repo is a stateless GORM
// wrapper). This matches the pre-334 pattern where the inline body
// constructed seriesCacheRepo once for the scan chain and a second
// time inside wireEnrichment.
//
// bgWG is the process-wide background wait group. ScanUC.WithWaitGroup
// stores the pointer so async scan goroutines (spawned by ScanUC.Run)
// block graceful shutdown's drainBackground.
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers (room for
// future seed-or-validate logic).
func BuildScan(
	persistence *PersistenceBundle,
	sonarrBundle *SonarrBundle,
	watchdogBundle *WatchdogBundle,
	cfg HTTPServeConfig,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) (*ScanBundle, error) {
	db := persistence.DB

	// F-4b-1: scanLog carries domain="scan" per §6.5. Applied at the wirer
	// once and passed to every component the scan context owns (ScanUC,
	// RescanUC, Sweeper). Evaluator + GrabUC stay on the bare `log` because
	// they are ALSO consumed by the regrab use case (wired elsewhere);
	// tagging them "scan" here would mistag regrab decisions.
	scanLog := sharedports.DomainLogger(log, "scan")

	scanRepo := repositories.NewScanRepository(db)
	decisionRepo := grabpersistence.NewDecisionRepository(db)
	grabRepo := grabpersistence.NewGrabRepository(db)
	cooldownRepo := watchdogpersistence.NewCooldownRepository(db)
	originRepo := enrichpersistence.NewOriginReleaseRepository(db)

	txr := repositories.NewGormTransactor(db)
	evaluator := evaluate.NewPerInstanceUseCase(decisionRepo, log)
	grabUC := grab.NewUseCase(grabRepo, cooldownRepo, originRepo, sonarr.Classifier{}, log).
		WithTransactor(txr)

	// seriesCacheRepo is local to this wirer — see godoc above.
	seriesRepo := enrichpersistence.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	// Story 380: season_stats writer was only wired into webhook.go and
	// seriesdetail.go before — the scan loop's fillSeriesCache never wrote
	// per-season counters, so DB stayed empty for any instance whose
	// webhook never fired. Mirrors the BuildWebhook pattern.
	seasonStatsRepo := repositories.NewSeasonStatsRepository(db)

	scanUC := scan.NewUseCase(sonarrBundle.ScanInstances, evaluator, scanRepo, scanLog, cfg.DryRun).
		WithGrabUseCase(grabUC).
		WithCooldowns(cooldownRepo).
		WithOrigins(originRepo).
		WithSeriesCache(seriesCacheRepo).
		WithSeasonStats(seasonStatsRepo).
		WithHealthRegistry(watchdogBundle.Checker.Registry()).
		WithWaitGroup(bgWG)

	rescanUC := rescan.NewUseCase(decisionRepo, grabRepo, scanRepo, scanUC, evaluator, sonarrBundle.Holder.Load, scanLog)

	sweepInterval := cfg.Scan.CooldownSweep
	if sweepInterval <= 0 {
		sweepInterval = 15 * time.Minute
	}
	sweeper := loops.NewSweepLoop(cooldownRepo, sweepInterval, scanLog)

	return &ScanBundle{
		Evaluator:    evaluator,
		GrabUC:       grabUC,
		ScanUC:       scanUC,
		RescanUC:     rescanUC,
		Sweeper:      sweeper,
		ScanRepo:     scanRepo,
		GrabRepo:     grabRepo,
		CooldownRepo: cooldownRepo,
		OriginRepo:   originRepo,
		DecisionRepo: decisionRepo,
		Txr:          txr,
	}, nil
}

// WebhookBundle groups the webhook-domain components constructed at boot.
// Returned by BuildWebhook. Threaded into:
//
//   - httpserver.NewServer (WebhookUC, Reconciler, StatusCache) — the
//     HTTP wirer remains in server.go for now.
//   - instance.UseCase chained setters (WithWebhookReconciler,
//     WithWebhookStatusCache) — server.go composes
//     `adapters.ReconcilerAdapter{Inner: webhookReconciler}` directly
//     via the bundle's ReconcilerAdapter field (pre-baked).
//   - loops.NewWebhookReconcileLoop (Reconciler, StatusCache) — the
//     background reconcile safety net (041d) is spawned by server.go
//     on the lifecycle group, same as cooldown-sweeper.
//   - torrentsync.NewReconciler / torrentsync.NewQuery (TorrentSeriesMapRepo)
//     — story 221 (A-3) wiring consumes the same repo.
//
// Field-level invariants:
//
//   - WebhookUC owns four reload-aware closures over sonarrBundle.Holder:
//     GUIDCooldownLookup, SonarrClientFor, InstanceFor, and (via Syncer)
//     Lookup. The holder is pointer-stable across reload (sonarr.go
//     §SonarrBundle), so every Load() call observes the most recent
//     snapshot fanout-published by SonarrClientsSubscriber.
//
//   - Syncer is the E-1 (Story 210) SeriesAdd path. Re-uses the same
//     holder.Load closure as the UC so a webhook event lands on the
//     freshly-reloaded instance map. It is wired into the UC via
//     Deps.SeriesSyncer; the Bundle exposes it for symmetry with
//     ScanBundle (downstream tests + future stories may want a handle).
//
//   - Reconciler reads through adapters.NewWebhookReconcileLookup over
//     sonarrBundle.InstanceReg. InstanceReg is built once in BuildSonarr
//     with `Load: holder.Load`, so the lookup is reload-aware by the
//     same mechanism.
//
//   - StatusCache is shared by Reconciler, the background reconcile loop
//     (loops.NewWebhookReconcileLoop), and the instance UC cleanup hook
//     (WithWebhookStatusCache). The Bundle's StatusCache pointer is the
//     SAME pointer every consumer holds.
//
//   - ReconcilerAdapter is pre-baked from the same Reconciler pointer.
//     instance.UseCase consumes it via WithWebhookReconciler (server.go
//     constructs instance.UseCase AFTER this bundle is built — not a
//     true late-bind, just an in-order chained setter).
//
//   - TorrentSeriesMapRepo + EpisodeStatesRepo are pre-Story-221/218
//     repos consumed both inside the UC (UpsertTx in the same tx as
//     UpdateTorrentHash; SeriesDelete cascade) and outside it
//     (torrentsync reconciler, torrentsync query). Exposed on the
//     bundle so server.go can pass the same pointer to both call sites.
type WebhookBundle struct {
	WebhookUC            *webhookuc.UseCase
	Syncer               *scan.Syncer
	Reconciler           *webhookinstall.Reconciler
	StatusCache          *webhookinstall.StatusCache
	ReconcilerAdapter    adapters.ReconcilerAdapter
	TorrentSeriesMapRepo *repositories.TorrentSeriesMapRepository
	EpisodeStatesRepo    *repositories.EpisodeStatesRepository
	SeasonStatsRepo      *repositories.SeasonStatsRepository
}

// BuildWebhook wires the webhook UC + Syncer + Reconciler + StatusCache
// stack.
//
// Construction order mirrors the pre-335 inline body verbatim:
//
//  1. EpisodeStatesRepo (Story 218 E-2 cascade) + TorrentSeriesMapRepo
//     (Story 221 A-3 bridge).
//  2. The 5 scan.Syncer-internal repos (episodes, episode_texts, genres,
//     genres_i18n, networks). These are stateless GORM wrappers — re-
//     constructing them here is free and they are not re-exposed on the
//     bundle (downstream consumers — seriesdetail, enrichment — build
//     their own instances from PersistenceBundle.DB).
//  3. scan.Syncer (Story 300 E-1 wiring fix) — captures
//     sonarrBundle.Holder.Load for reload-aware lookup.
//  4. webhook.UseCase with the 4 reload-aware closures.
//  5. webhookinstall.StatusCache.
//  6. webhookinstall.Reconciler — reads through
//     adapters.NewWebhookReconcileLookup(sonarrBundle.InstanceReg).
//  7. adapters.ReconcilerAdapter pre-baked over the Reconciler pointer.
//
// seriesRepo + seriesCacheRepo are NOT inputs because the wirer
// constructs them locally — same pattern as ScanBundle. They are
// stateless GORM wrappers; building a second instance here matches the
// pre-335 inline body (server.go line 253-254 already built one for
// the seriesdetail block; the webhook block built its own
// indirectly through the Syncer Deps).
//
// cfg is the HTTPServeConfig from BuildRuntimeConfig — the wirer only
// reads cfg.HTTP.Auth.APIKey to seed the reconciler's API-key header.
//
// scanBundle is reserved — currently unused (the webhook UC depends on
// GrabRepo, CooldownRepo from scanBundle via the same Bundle that
// holds them, but the inline body constructs them through scanBundle
// fields). Passed in for symmetry + future-proofing (if the wirer
// later needs Evaluator / Txr from the scan stack).
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers (room for
// future seed-or-validate logic).
func BuildWebhook(
	persistence *PersistenceBundle,
	sonarrBundle *SonarrBundle,
	scanBundle *ScanBundle,
	cfg HTTPServeConfig,
	log *slog.Logger,
) (*WebhookBundle, error) {
	_ = scanBundle // reserved — see godoc
	db := persistence.DB
	holder := sonarrBundle.Holder

	// F-4b-4: webhookLog carries domain="webhook" per §6.5. Applied at
	// the wirer once and passed to every component the webhook context
	// owns: the webhook-scoped scan.Syncer instance (Deps.Logger +
	// Syncer.Logger fields), the webhook.UseCase, and the
	// webhookinstall.Reconciler. The WebhookReconcileLoop is wired in
	// server.go (NOT here) — see story 395 Q7. The Syncer instance
	// constructed here is webhook-scoped (its Lookup closure resolves
	// Sonarr clients only on behalf of webhook SeriesAdd events;
	// production scan-side wiring does not construct a Syncer at all),
	// so "webhook" is the correct domain for its records.
	webhookLog := sharedports.DomainLogger(log, "webhook")

	// Story 218 (E-2) — webhook SeriesDelete cascade soft-deletes
	// episode_states under the deleted series. Repo is constructed
	// here so the cascade port is wired at boot.
	webhookEpisodeStatesRepo := repositories.NewEpisodeStatesRepository(db)
	webhookSeasonStatsRepo := repositories.NewSeasonStatsRepository(db)
	// 221 (A-3) — torrent_series_map repo wired here so the webhook
	// path can write the bridge row in the same tx as the
	// grab_records.torrent_hash update. Repo also feeds the
	// torrentsync reconciler constructed later in server.go.
	torrentSeriesMapRepo := repositories.NewTorrentSeriesMapRepository(db)

	// Story 300 (E-1 wiring fix) — construct scan.Syncer so the
	// webhook SeriesAdd path populates the canonical entity model
	// (series + episodes + episode_states + series_genres +
	// series_networks) instead of falling back to the thin
	// CacheEntry write. Repos are stateless GORM wrappers (same
	// shape as the Story 215 seriesdetail block), so re-
	// constructing them here is free. Lookup returns the concrete
	// *sonarr.Client because Syncer.SyncFromSonarrAPI needs the
	// payload-fetcher methods (GetSeriesPayload / ListEpisodesForSync
	// / ListEpisodeFilesForSync) that live on the concrete type,
	// not on ports.SonarrClient. Unknown instance OR a non-concrete
	// client → (nil, false), webhook silently falls back to the
	// pre-E-1 thin CacheEntry path.
	seriesRepo := enrichpersistence.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	webhookEpisodesRepo := enrichpersistence.NewEpisodesRepository(db)
	webhookEpisodeTextsRepo := enrichpersistence.NewEpisodeTextsRepository(db)
	webhookGenresRepo := enrichpersistence.NewGenresRepository(db)
	webhookGenresI18nRepo := enrichpersistence.NewGenresI18nRepository(db)
	webhookNetworksRepo := enrichpersistence.NewNetworksRepository(db)
	webhookSeriesSyncer := &scan.Syncer{
		Deps: scan.SyncDeps{
			Series:        seriesRepo,
			SeriesCache:   seriesCacheRepo,
			Episodes:      webhookEpisodesRepo,
			EpisodeStates: webhookEpisodeStatesRepo,
			EpisodeTexts:  webhookEpisodeTextsRepo,
			SeasonStats:   webhookSeasonStatsRepo,
			Genres:        scan.NewGenresAdapter(webhookGenresRepo, webhookGenresI18nRepo),
			Networks:      scan.NewNetworksAdapter(webhookNetworksRepo),
			Logger:        webhookLog,
		},
		Lookup: func(name domain.InstanceName) (*sonarr.Client, bool) {
			h := holder.Load()
			if h == nil {
				return nil, false
			}
			inst, ok := h[string(name)]
			if !ok || inst.Client == nil {
				return nil, false
			}
			concrete, ok := inst.Client.(*sonarr.Client)
			if !ok {
				return nil, false
			}
			return concrete, true
		},
		Logger: webhookLog,
	}

	// scan stack repos come from scanBundle (GrabRepo, CooldownRepo);
	// SeriesCacheRepo is the local one (matches pre-335: the inline
	// body re-used the same seriesCacheRepo built at server.go:254).
	webhookUC := webhookuc.New(webhookuc.Deps{
		Grabs:            scanBundle.GrabRepo,
		Cooldowns:        scanBundle.CooldownRepo,
		SeriesCache:      seriesCacheRepo,
		Tx:               scanBundle.Txr,
		EpisodeStates:    webhookEpisodeStatesRepo,
		SeasonStats:      webhookSeasonStatsRepo,
		TorrentSeriesMap: torrentSeriesMapRepo,
		SeriesSyncer:     webhookSeriesSyncer,
		GUIDCooldownLookup: func(name domain.InstanceName) time.Duration {
			inst, ok := holder.Load()[string(name)]
			if !ok {
				return 0
			}
			return inst.Config.Cooldown.GUIDAfterFailedImport
		},
		Logger: webhookLog,
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

	webhookStatusCache := webhookinstall.NewStatusCache()
	webhookReconciler := webhookinstall.New(webhookinstall.Deps{
		Lookup:    adapters.NewWebhookReconcileLookup(sonarrBundle.InstanceReg),
		PublicURL: webhookinstall.PublicURLFromContext,
		Cache:     webhookStatusCache,
		APIKey:    cfg.HTTP.Auth.APIKey,
		Logger:    webhookLog,
	})

	return &WebhookBundle{
		WebhookUC:            webhookUC,
		Syncer:               webhookSeriesSyncer,
		Reconciler:           webhookReconciler,
		StatusCache:          webhookStatusCache,
		ReconcilerAdapter:    adapters.ReconcilerAdapter{Inner: webhookReconciler},
		TorrentSeriesMapRepo: torrentSeriesMapRepo,
		EpisodeStatesRepo:    webhookEpisodeStatesRepo,
		SeasonStatsRepo:      webhookSeasonStatsRepo,
	}, nil
}

// RegrabBundle groups the Phase 10 Watchdog components constructed at
// boot. Returned by BuildRegrab. Threaded into:
//
//   - httpserver.NewServer (QbitSettingsHandler, WatchdogRollupHandler,
//     WatchdogBlacklistHandler, WatchdogSeasonsHandler,
//     WebhooksAggregateHandler) — the HTTP wirer remains in server.go.
//   - startSubscribers (QbitLoader → qbitSettingsLoader contract; the
//     RegrabLoop pointer satisfies the regrabSwapper contract).
//   - server.go calls RegrabLoop.Start(rootCtx) directly because the
//     loop owner needs the cancellation-bearing rootCtx, which the
//     wirer does not (and should not) own.
//
// Field-level invariants:
//
//   - QbitSettingsUC owns the Lookup contract consumed by RegrabUC and
//     the WatchdogRollupHandler / WatchdogSeasonsHandler. Built first;
//     every downstream consumer holds the same pointer.
//
//   - BlacklistRepo + NoBetterCounterRepo are constructed locally and
//     re-exposed because both the regrab use case and the watchdog
//     handlers consume them (the inline pre-337 body built them once
//     and shared by name).
//
//   - RegrabUC is the orchestrator. WithMetrics + WithDecisions are
//     applied here so callers see a fully-configured handle.
//
//   - RegrabLoop is constructed here but NOT started — server.go owns
//     rootCtx, calls .Start(rootCtx) inline after BuildRegrab returns.
//     The pointer satisfies cmd/server.regrabSwapper for the reload
//     fanout via its SwapSettings method.
//
//   - QbitLoader is a function-typed shim (adapters.QbitSettingsLoaderFunc).
//     It closes over qbitSettingsRepo, instanceRepo, cipher so every
//     bus.Publish-driven refresh re-reads the most recent rows; the
//     closure is reload-safe by construction (no captured snapshot).
//
//   - WatchdogRollupHandler holds the QbitProbe + QbitTorrentsLister
//     production adapters (infraregrab.QbitProbeFunc{} /
//     QbitTorrentsListerFunc{}). WithQbitProbe / WithQbitTorrentsLister
//     are applied in the same chain as the pre-337 inline body.
//
//   - WebhooksAggregateHandler is a thin wrapper over the webhook
//     reconciler + instance lister. Lives here (not in webhook.go)
//     because it shares the same watchdogInstanceAdapter with the
//     other Phase 10 handlers — keeping the construction site
//     together preserves the pre-337 pattern of "all Phase 10 wiring
//     in one block".
type RegrabBundle struct {
	QbitSettingsUC           *regrab.SettingsUseCase
	QbitSettingsHandler      *handlers.QbitSettingsHandler
	BlacklistRepo            *repositories.WatchdogBlacklistRepository
	NoBetterCounterRepo      *watchdogpersistence.NoBetterCounterRepository
	RegrabUC                 *regrab.UseCase
	RegrabLoop               *loops.RegrabLoop
	WatchdogRollupHandler    *watchdogrest.WatchdogRollupHandler
	WatchdogBlacklistHandler *watchdogrest.WatchdogBlacklistHandler
	WatchdogSeasonsHandler   *watchdogrest.WatchdogSeasonsHandler
	WebhooksAggregateHandler *handlers.WebhooksAggregateHandler
	QbitLoader               adapters.QbitSettingsLoaderFunc
}

// BuildRegrab wires the Phase 10 Watchdog stack.
//
// Construction order mirrors the pre-337 inline body verbatim:
//
//  1. qbitSettingsRepo + qbitSettingsUC + qbitSettingsHandler.
//  2. blacklistRepo + noBetterCounterRepo + regrabUC (WithMetrics +
//     WithDecisions).
//  3. RegrabLoop (NewRegrabLoop) — NOT started here; server.go calls
//     .Start(rootCtx) after BuildRegrab returns.
//  4. watchdogInstanceAdapter + WatchdogRollupHandler (WithQbitProbe +
//     WithQbitTorrentsLister).
//  5. seriesRepo + seriesCacheRepo (local — same pattern as scan.go /
//     webhook.go; stateless GORM wrappers, rebuilt on demand).
//  6. WatchdogBlacklistHandler.
//  7. watchdogSeasonsRepo + WatchdogSeasonsHandler.
//  8. WebhooksAggregateHandler.
//  9. QbitLoader closure (captures qbitSettingsRepo + instanceRepo +
//     cipher). Reload-safe by construction.
//
// bgWG is the process-wide background wait group — forwarded to
// loops.NewRegrabLoop so the per-instance polling goroutines block
// graceful shutdown's drainBackground.
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers.
func BuildRegrab(
	persistence *PersistenceBundle,
	sonarrBundle *SonarrBundle,
	scanBundle *ScanBundle,
	webhookBundle *WebhookBundle,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) (*RegrabBundle, error) {
	db := persistence.DB
	cipher := persistence.Cipher
	instanceRepo := persistence.InstanceRepo

	// F-4b-3: watchdogLog carries domain="watchdog" per §6.5 (Phase 10
	// Watchdog re-uses the existing "watchdog" closed-list value — see
	// PRD §6.5 sub-context re-use paragraph). Applied at the wirer once
	// and passed to every component the regrab/watchdog context owns:
	// SettingsUseCase, regrab.UseCase, RegrabLoop, and the qbitLoader
	// closure's WarnContext sites. The five HTTP handlers (QbitSettings,
	// WatchdogRollup, WatchdogBlacklist, WatchdogSeasons,
	// WebhooksAggregate) stay on bare `log` because handlers belong to
	// the future F-4b-N handlers slice and will use LoggerFromContext(ctx)
	// (request scope already carries domain="http"), not DomainLogger.
	watchdogLog := sharedports.DomainLogger(log, "watchdog")

	// Phase 10 Watchdog — settings CRUD.
	qbitSettingsRepo := repositories.NewQbitSettingsRepository(db)
	qbitSettingsUC := regrab.NewSettingsUseCase(qbitSettingsRepo, instanceRepo, cipher, watchdogLog).
		WithWebhookChecker(adapters.NewWebhookChecker(sonarrBundle.InstanceReg))
	// HTTP handler stays on bare `log` — see watchdogLog godoc above.
	qbitSettingsHandler := handlers.NewQbitSettingsHandler(qbitSettingsUC, log)

	// regrab orchestrator — Phase 10 core. Depends on the settings use
	// case (Lookup), instance registry (Get), qBit + detector factories,
	// grab / cooldown / blacklist / counter repos, evaluator + grab UC.
	blacklistRepo := repositories.NewWatchdogBlacklistRepository(db)
	noBetterCounterRepo := watchdogpersistence.NewNoBetterCounterRepository(db)
	regrabUC := regrab.NewUseCase(
		qbitSettingsUC, // implements SettingsLookup
		sonarrBundle.InstanceRegistry,
		infraregrab.QbitClientFactoryFunc{},
		infraregrab.DetectorFactoryFunc{},
		scanBundle.GrabRepo, scanBundle.CooldownRepo, blacklistRepo, noBetterCounterRepo,
		scanBundle.Evaluator, scanBundle.GrabUC,
		watchdogLog,
	).WithMetrics(observability.WatchdogMetricsAdapter{}).
		WithDecisions(scanBundle.DecisionRepo)

	// RegrabLoop owns per-instance polling goroutines; SwapSettings is
	// called from the OnApplied fanout. NOT started here — server.go
	// owns rootCtx and calls .Start(rootCtx) inline after BuildRegrab
	// returns.
	regrabLoop := loops.NewRegrabLoop(regrabUC, observability.WatchdogMetricsAdapter{}, bgWG, watchdogLog)

	// 047a — watchdog rollup handler.
	watchdogInstanceAdapter := adapters.NewWatchdogInstanceLister(instanceRepo, cipher)
	watchdogRollupHandler := watchdogrest.NewWatchdogRollupHandler(
		qbitSettingsUC,          // SettingsLookup
		regrabUC,                // RollupSnapshotProvider
		scanBundle.GrabRepo,     // rollupGrabCounter
		blacklistRepo,           // rollupBlacklistCounter
		watchdogInstanceAdapter, // InstanceLister
		watchdogInstanceAdapter, // InstanceIDLookup
		log,
	).WithQbitProbe(infraregrab.QbitProbeFunc{}).
		WithQbitTorrentsLister(infraregrab.QbitTorrentsListerFunc{})

	// 047b — blacklist handler. seriesRepo + seriesCacheRepo are local
	// (stateless GORM wrappers, same pattern as scan.go / webhook.go).
	seriesRepo := enrichpersistence.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	watchdogBlacklistHandler := watchdogrest.NewWatchdogBlacklistHandler(
		blacklistRepo,           // BlacklistPager
		seriesCacheRepo,         // SeriesTitleResolver
		watchdogInstanceAdapter, // InstanceIDLookup
		log,
	)

	// 098a — watchdog seasons aggregate read view.
	watchdogSeasonsRepo := repositories.NewWatchdogSeasonsRepository(db)
	watchdogSeasonsHandler := watchdogrest.NewWatchdogSeasonsHandler(
		watchdogSeasonsRepo,
		watchdogSeasonsRepo,
		qbitSettingsUC,
		log,
	)

	webhooksAggregateHandler := handlers.NewWebhooksAggregateHandler(
		webhookBundle.Reconciler,
		watchdogInstanceAdapter, // InstanceLister
		log,
	)

	// qBit settings loader for the OnApplied fanout. Calls List + builds
	// the Settings map fresh on every publish. The Lookup closure
	// delegates to qbitSettingsUC so password decryption is centralised.
	// Reload-safe by construction — no captured snapshot, every Load
	// re-reads the live repos.
	qbitLoader := adapters.QbitSettingsLoaderFunc(func(ctx context.Context) map[string]regrab.Settings {
		recs, err := qbitSettingsRepo.List(ctx)
		if err != nil {
			watchdogLog.WarnContext(ctx, "qbit_settings_list_failed",
				slog.String("error", err.Error()))
			return map[string]regrab.Settings{}
		}
		out := make(map[string]regrab.Settings, len(recs))
		instances, err := instanceRepo.List(ctx, cipher)
		if err != nil {
			watchdogLog.WarnContext(ctx, "qbit_settings_list_instances_failed",
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
			s, err := regrab.NewSettingsFromRecord(rec, domain.InstanceName(name), cipher)
			if err != nil {
				watchdogLog.WarnContext(ctx, "qbit_settings_decrypt_failed",
					slog.String("instance", name),
					slog.String("error", err.Error()))
				continue
			}
			out[name] = s
		}
		return out
	})

	return &RegrabBundle{
		QbitSettingsUC:           qbitSettingsUC,
		QbitSettingsHandler:      qbitSettingsHandler,
		BlacklistRepo:            blacklistRepo,
		NoBetterCounterRepo:      noBetterCounterRepo,
		RegrabUC:                 regrabUC,
		RegrabLoop:               regrabLoop,
		WatchdogRollupHandler:    watchdogRollupHandler,
		WatchdogBlacklistHandler: watchdogBlacklistHandler,
		WatchdogSeasonsHandler:   watchdogSeasonsHandler,
		WebhooksAggregateHandler: webhooksAggregateHandler,
		QbitLoader:               qbitLoader,
	}, nil
}

// TorrentsyncBundle groups the qBit torrent-sync components constructed at
// boot. Returned by BuildTorrentsync. Threaded into:
//
//   - httpserver.NewServer (SeriesTorrentsHandler) — the HTTP wirer
//     remains in server.go for now.
//   - startSubscribers (Loop pointer satisfies the torrentsyncSwapper
//     contract via SwapSettings).
//   - server.go calls Loop.Start(rootCtx) directly because the loop
//     owner needs the cancellation-bearing rootCtx, which the wirer
//     does not (and should not) own.
//
// Field-level invariants:
//
//   - Store is the in-memory store consumed by both the UC and the Query.
//
//   - Policy is the persist policy fed to the UC; it captures the
//     qbit_torrents + qbit_torrent_events repos.
//
//   - Factory is the production session factory adapter — returned as
//     the torrentsync.SyncSessionFactory interface so it threads
//     directly into NewUseCase. Closes over regrabBundle.QbitSettingsUC
//     for password-decrypting Lookup.
//
//   - Reconciler is built with the same TorrentSeriesMapRepo as the
//     webhook UC (story 335 invariant: one repo pointer, two consumers).
//     sonarrFor closure reads through sonarrBundle.Holder.Load, so it
//     observes the live instance map after every reload publish.
//
//   - UC is the orchestrator. WithReconciler is applied here so callers
//     see a fully-configured handle.
//
//   - Loop is constructed here but NOT started — server.go owns rootCtx
//     and calls .Start(rootCtx) after BuildTorrentsync returns.
//
//   - Query is the read-side companion to the UC. It re-uses the same
//     Store pointer + qbit_torrents repo + TorrentSeriesMapRepo.
//
//   - SeriesTorrentsHandler wraps Query for the per-series torrents
//     endpoint (story 222 / A-4). Holds local seriesRepo +
//     seriesCacheRepo handles (stateless GORM wrappers, same pattern as
//     webhook.go + regrab.go).
type TorrentsyncBundle struct {
	Store                 *torrentsync.Store
	Policy                *torrentsync.PersistPolicy
	Factory               torrentsync.SyncSessionFactory
	Reconciler            *torrentsync.Reconciler
	UC                    *torrentsync.UseCase
	Loop                  *loops.TorrentsyncLoop
	Query                 *torrentsync.Query
	SeriesTorrentsHandler *handlers.SeriesTorrentsHandler
}

// BuildTorrentsync wires the torrentsync stack (220 A-2 + 221 A-3 +
// 222 A-4 in the pre-338 inline body).
//
// Construction order mirrors the pre-338 inline body verbatim:
//
//  1. qbit_torrents + qbit_torrent_events repos.
//  2. Store + PersistPolicy.
//  3. SessionFactory adapter (closes over regrabBundle.QbitSettingsUC).
//  4. sonarrFor closure over sonarrBundle.Holder.Load — reload-aware
//     by construction.
//  5. Reconciler (uses webhookBundle.TorrentSeriesMapRepo and
//     scanBundle.GrabRepo).
//  6. UseCase (with WithReconciler).
//  7. Loop (NewTorrentsyncLoop + NewProductionTorrentsyncRunner) —
//     NOT started here; server.go owns rootCtx.
//  8. Query (re-uses TorrentSeriesMapRepo as LookupRepo).
//  9. SeriesTorrentsHandler — seriesRepo + seriesCacheRepo are local
//     (stateless GORM wrappers).
//
// bgWG is the process-wide background wait group — forwarded to
// loops.NewTorrentsyncLoop so the per-instance polling goroutines
// block graceful shutdown's drainBackground.
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers.
func BuildTorrentsync(
	persistence *PersistenceBundle,
	sonarrBundle *SonarrBundle,
	scanBundle *ScanBundle,
	webhookBundle *WebhookBundle,
	regrabBundle *RegrabBundle,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) (*TorrentsyncBundle, error) {
	db := persistence.DB
	holder := sonarrBundle.Holder

	// F-4b-2: qbitLog carries domain="qbit" per §6.5. Applied at the wirer
	// once and passed to every component the torrentsync context owns
	// (PersistPolicy, Reconciler, UseCase, ProductionTorrentsyncRunner,
	// TorrentsyncLoop). The SeriesTorrentsHandler stays on bare `log`
	// because HTTP handlers belong to the future F-4b-N handlers slice
	// and will use LoggerFromContext(ctx) (request scope already carries
	// domain="http"), not DomainLogger.
	qbitLog := sharedports.DomainLogger(log, "qbit")

	// 220 (A-2) — qbit_torrents + qbit_torrent_events repos.
	qbitTorrentsRepo := repositories.NewQbitTorrentsRepository(db)
	qbitTorrentEventsRepo := repositories.NewQbitTorrentEventsRepository(db)

	// Store + PersistPolicy.
	store := torrentsync.NewStore()
	policy := torrentsync.NewPersistPolicy(qbitTorrentsRepo, qbitTorrentEventsRepo, qbitLog)

	// Session factory adapter — closes over regrabBundle.QbitSettingsUC
	// for password-decrypting Lookup. Returned as the
	// torrentsync.SyncSessionFactory interface so it threads into
	// NewUseCase without a cast.
	factory := loops.NewTorrentsyncSessionFactoryAdapter(
		infraregrab.QbitClientFactoryFunc{},
		regrabBundle.QbitSettingsUC,
	)

	// 221 (A-3) — sonarrFor closure wires the per-instance Sonarr
	// client lookup the reconciler needs for sources 3 + 4. Production
	// wiring reuses the instance holder; the concrete *sonarr.Client
	// satisfies torrentsync.SonarrReconciler (its QueueAll +
	// GrabHistoryPaged are exactly the two methods in the port).
	sonarrFor := func(instance domain.InstanceName) (torrentsync.SonarrReconciler, bool) {
		h := holder.Load()
		inst, ok := h[string(instance)]
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
		store,
		webhookBundle.TorrentSeriesMapRepo,
		scanBundle.GrabRepo,
		sonarrFor,
		observability.TorrentsyncMetricsAdapter{},
		qbitLog,
	)

	useCase := torrentsync.NewUseCase(
		store, policy,
		factory, qbitTorrentsRepo, qbitLog,
	).WithReconciler(reconciler)

	// Loop owns per-instance polling goroutines; SwapSettings is
	// called from the OnApplied fanout. NOT started here — server.go
	// owns rootCtx and calls .Start(rootCtx) inline after
	// BuildTorrentsync returns.
	loop := loops.NewTorrentsyncLoop(
		loops.NewProductionTorrentsyncRunner(useCase, qbitLog),
		bgWG, qbitLog,
	)

	// 222 (A-4) — per-series torrents endpoint. Reuses the store +
	// qbit_torrents repo wired above. TorrentSeriesMapRepo is shared
	// with the reconciler (story 335 invariant).
	query := torrentsync.NewQuery(store, qbitTorrentsRepo, webhookBundle.TorrentSeriesMapRepo)

	// seriesRepo + seriesCacheRepo are local (stateless GORM wrappers,
	// same pattern as webhook.go / regrab.go — re-constructing them
	// here is free and mirrors the pre-338 inline body which captured
	// the seriesdetail-block instances).
	seriesRepo := enrichpersistence.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	// HTTP handler stays on bare `log` — see qbitLog godoc above.
	seriesTorrentsHandler := handlers.NewSeriesTorrentsHandler(
		query, seriesCacheRepo, seriesRepo, log,
	)

	return &TorrentsyncBundle{
		Store:                 store,
		Policy:                policy,
		Factory:               factory,
		Reconciler:            reconciler,
		UC:                    useCase,
		Loop:                  loop,
		Query:                 query,
		SeriesTorrentsHandler: seriesTorrentsHandler,
	}, nil
}
