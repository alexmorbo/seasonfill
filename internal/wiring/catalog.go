package wiring

import (
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/internal/admin/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/instance"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/rescan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	webhookuc "github.com/alexmorbo/seasonfill/internal/catalog/app/webhook"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/webhookinstall"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/config"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	grab "github.com/alexmorbo/seasonfill/internal/grab/app"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
	"github.com/alexmorbo/seasonfill/internal/shared/reload"
	infraregrab "github.com/alexmorbo/seasonfill/internal/watchdog/infrastructure/regrab"
	watchdogpersistence "github.com/alexmorbo/seasonfill/internal/watchdog/persistence"
)

// catalog.go owns the wiring for the catalog bounded context:
// per-instance Sonarr clients (Story 332), the scan/grab/rescan
// stack (Story 334), the webhook UC + Syncer + Reconciler + StatusCache
// (Story 335), the torrentsync UC + reconciler + query (Stories
// 220/221/222), and the catalog-side HTTP handlers (instance CRUD +
// probe — Story 336).
//
// Per-context split per PRD §3.2 (Story 452). The original A-2 layout
// concentrated every wirer into 5 files (persistence/integrations/
// loops/httpiface/runtime); the per-context layout pushes each Build*
// wirer into the file matching its bounded context so future stories
// touch one file per change.

// SonarrBundle holds the per-instance Sonarr wiring shared between the
// HTTP layer, the scan / watchdog / regrab use cases, and the reload
// bus. Constructed once at boot by BuildSonarr; mutated downstream
// only via the Holder (the reload OnApplied fanout calls Replace).
//
// Field-level invariants — see story 332 §Risks for the rationale:
//
//   - Holder is a pointer-typed handle. Its identity is preserved
//     across reload — every reload-aware closure (4 call sites in
//     scan.Syncer.Lookup, webhook UC GUIDCooldownLookup /
//     SonarrClientFor / InstanceFor, seriesdetail SonarrFor,
//     torrentsync sonarrFor) reads through Holder.Load and observes
//     whichever snapshot the fanout published.
//
//   - GlobalLimiterPtr is a pointer to a heap-allocated atomic. The
//     ClientFactory captures it at construction; the
//     GlobalRateLimiterSubscriber Stores into it on reload; the
//     testcontext hook reads from it. Same atomic cell everywhere.
//
//   - InstanceRegistry is a value (not pointer) per design doc §3.3.
//     It is a thin adapter struct that captures InstanceReg and
//     delegates Get() through InstanceReg.Load().
type SonarrBundle struct {
	ClientFactory       reload.SonarrClientFactory
	ClientsByName       map[string]ports.SonarrClient
	SonarrClients       []ports.SonarrClient
	ScanInstances       []scan.Instance
	ScanInstancesByName map[string]scan.Instance
	CfgByName           map[string]config.HealthCheckConfig
	Holder              *adapters.InstanceMapHolder
	InstanceReg         catalogrest.InstanceRegistry
	InstanceRegistry    adapters.RegrabInstanceRegistry
	GlobalLimiterPtr    *atomic.Pointer[ratelimit.Limiter]
}

// BuildSonarr seeds the global rate-limiter pointer, builds the
// production SonarrClientFactory closure, instantiates the boot client
// set, materialises the scan.Instance views (slice + by-name map +
// HealthCheck cfg by-name), wraps everything in an InstanceMapHolder
// for reload-aware lookups, and adapts the holder to the handler-side
// InstanceRegistry + regrab-side RegrabInstanceRegistry.
//
// The returned bundle is the single source of truth for per-instance
// Sonarr wiring; every downstream consumer (scan UC, watchdog,
// regrab, webhook UC, healthcheck, seriesdetail composer, torrentsync
// reconciler, HTTP handlers) reads from the bundle's handles instead
// of re-deriving them.
//
// snap is the runtime configuration snapshot from BuildRuntimeConfig.
// The bundle reads snap.GlobalRateLimit (to seed the limiter) and
// snap.Instances (to enumerate per-instance clients).
//
// No error path — every step is in-memory construction. The signature
// returns error to leave room for future seed-or-validate logic
// without a downstream signature churn.
func BuildSonarr(snap runtime.Snapshot, log *slog.Logger) (*SonarrBundle, error) {
	// Single shared global limiter pointer (live-reloaded). Heap-
	// allocate the atomic so its address is stable across the
	// function return — the ClientFactory captures &limiterPtr and
	// the GlobalRateLimiterSubscriber Stores into it on reload.
	// Seed from the boot snapshot so the first publish's subscriber
	// diff-skip works.
	limiterPtr := new(atomic.Pointer[ratelimit.Limiter])
	limiterPtr.Store(reload.DefaultGlobalLimiterFactory(
		snap.GlobalRateLimit.RPM, snap.GlobalRateLimit.Burst))

	clientFactory := reload.NewSonarrClientFactory(limiterPtr, log)

	n := len(snap.Instances)
	clientsByName := make(map[string]ports.SonarrClient, n)
	for _, sc := range snap.Instances {
		clientsByName[sc.Name] = clientFactory(sc)
	}

	sonarrClients := make([]ports.SonarrClient, 0, n)
	scanInstances := make([]scan.Instance, 0, n)
	scanInstancesByName := make(map[string]scan.Instance, n)
	cfgByName := make(map[string]config.HealthCheckConfig, n)
	for _, sc := range snap.Instances {
		c := clientsByName[sc.Name]
		sonarrClients = append(sonarrClients, c)
		si := scan.Instance{Config: sc, Client: c}
		scanInstances = append(scanInstances, si)
		scanInstancesByName[sc.Name] = si
		cfgByName[sc.Name] = config.NewHealthCheckConfig(sc.HealthCheck)
	}

	holder := adapters.NewInstanceMapHolder(scanInstancesByName)

	// InstanceReg is a handler-side accessor: its Load closure is
	// reload-aware via the holder. Build it once here so every
	// downstream caller (webhookReconciler, qbitSettingsUC,
	// httpServer.NewServer, regrabUC) reads through the same
	// value — no per-site reconstruction.
	instanceReg := catalogrest.InstanceRegistry{Load: holder.Load}

	return &SonarrBundle{
		ClientFactory:       clientFactory,
		ClientsByName:       clientsByName,
		SonarrClients:       sonarrClients,
		ScanInstances:       scanInstances,
		ScanInstancesByName: scanInstancesByName,
		CfgByName:           cfgByName,
		Holder:              holder,
		InstanceReg:         instanceReg,
		InstanceRegistry:    adapters.NewRegrabInstanceRegistry(instanceReg),
		GlobalLimiterPtr:    limiterPtr,
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
	ScanRepo     *catalogpersistence.ScanRepository
	GrabRepo     *grabpersistence.GrabRepository
	CooldownRepo *watchdogpersistence.CooldownRepository
	OriginRepo   *grabpersistence.OriginReleaseRepository
	DecisionRepo *grabpersistence.DecisionRepository
	Txr          *catalogpersistence.GormTransactor
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

	scanRepo := catalogpersistence.NewScanRepository(db)
	decisionRepo := grabpersistence.NewDecisionRepository(db)
	grabRepo := grabpersistence.NewGrabRepository(db)
	cooldownRepo := watchdogpersistence.NewCooldownRepository(db)
	originRepo := grabpersistence.NewOriginReleaseRepository(db)

	txr := catalogpersistence.NewGormTransactor(db)
	evaluator := evaluate.NewPerInstanceUseCase(decisionRepo, log)
	grabUC := grab.NewUseCase(grabRepo, cooldownRepo, originRepo, sonarr.Classifier{}, log).
		WithTransactor(txr)

	// seriesCacheRepo is local to this wirer — see godoc above.
	seriesRepo := enrichpersistence.NewSeriesRepository(db)
	seriesCacheRepo := catalogpersistence.NewSeriesCacheRepository(db, seriesRepo).
		WithSeriesTexts(enrichpersistence.NewSeriesTextsRepository(db))
	// Story 380: season_stats writer was only wired into webhook.go and
	// seriesdetail.go before — the scan loop's fillSeriesCache never wrote
	// per-season counters, so DB stayed empty for any instance whose
	// webhook never fired. Mirrors the BuildWebhook pattern.
	seasonStatsRepo := catalogpersistence.NewSeasonStatsRepository(db)

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
	TorrentSeriesMapRepo *catalogpersistence.TorrentSeriesMapRepository
	EpisodeStatesRepo    *catalogpersistence.EpisodeStatesRepository
	SeasonStatsRepo      *catalogpersistence.SeasonStatsRepository
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
	webhookEpisodeStatesRepo := catalogpersistence.NewEpisodeStatesRepository(db)
	webhookSeasonStatsRepo := catalogpersistence.NewSeasonStatsRepository(db)
	// 221 (A-3) — torrent_series_map repo wired here so the webhook
	// path can write the bridge row in the same tx as the
	// grab_records.torrent_hash update. Repo also feeds the
	// torrentsync reconciler constructed later in server.go.
	torrentSeriesMapRepo := catalogpersistence.NewTorrentSeriesMapRepository(db)

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
	seriesCacheRepo := catalogpersistence.NewSeriesCacheRepository(db, seriesRepo).
		WithSeriesTexts(enrichpersistence.NewSeriesTextsRepository(db))
	webhookEpisodesRepo := enrichpersistence.NewEpisodesRepository(db)
	webhookEpisodeTextsRepo := enrichpersistence.NewEpisodeTextsRepository(db)
	webhookSeriesTextsRepo := enrichpersistence.NewSeriesTextsRepository(db) // S-E1 base-lang writer
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
			SeriesTexts:   webhookSeriesTextsRepo, // S-E1
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
	SeriesTorrentsHandler *seriesdetailrest.SeriesTorrentsHandler
	// QbitCapacityLoop is the B-32 periodic qbit_torrents row-count
	// collector. server.go owns rootCtx and calls .Run on it under
	// bgWG, mirroring the TorrentsyncLoop.Start pattern.
	QbitCapacityLoop *loops.QbitCapacityLoop
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
	qbitTorrentsRepo := catalogpersistence.NewQbitTorrentsRepository(db)
	qbitTorrentEventsRepo := catalogpersistence.NewQbitTorrentEventsRepository(db)

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
	// B-32 — single TorrentsyncMetricsAdapter value satisfies the
	// reconciler's narrow UnmappedGauge AND the use-case + loop wider
	// Metrics port. One sink, no double-emission risk.
	torrentsyncMetrics := observability.TorrentsyncMetricsAdapter{}

	reconciler := torrentsync.NewReconciler(
		store,
		webhookBundle.TorrentSeriesMapRepo,
		scanBundle.GrabRepo,
		sonarrFor,
		torrentsyncMetrics,
		qbitLog,
	)

	useCase := torrentsync.NewUseCase(
		store, policy,
		factory, qbitTorrentsRepo, qbitLog,
	).WithReconciler(reconciler).WithMetrics(torrentsyncMetrics)

	// Loop owns per-instance polling goroutines; SwapSettings is
	// called from the OnApplied fanout. NOT started here — server.go
	// owns rootCtx and calls .Start(rootCtx) inline after
	// BuildTorrentsync returns.
	loop := loops.NewTorrentsyncLoop(
		loops.NewProductionTorrentsyncRunnerWithMetrics(useCase, torrentsyncMetrics, qbitLog),
		bgWG, qbitLog,
	)

	// B-32 — qbit_torrents row-count collector. Instance source is
	// a closure over the sonarr holder so OnApplied publishes are
	// reflected on the next 60s tick. NOT started here; server.go
	// bumps bgWG and runs it under rootCtx, mirroring TorrentsyncLoop.
	capacityLoop := loops.NewQbitCapacityLoop(
		qbitTorrentsRepo,
		loops.QbitCapacityInstancesFunc(func() []domain.InstanceName {
			snap := holder.Load()
			out := make([]domain.InstanceName, 0, len(snap))
			for name := range snap {
				out = append(out, domain.InstanceName(name))
			}
			return out
		}),
		observability.QbitCapacityMetricsAdapter{},
		loops.DefaultQbitCapacityInterval,
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
	seriesCacheRepo := catalogpersistence.NewSeriesCacheRepository(db, seriesRepo).
		WithSeriesTexts(enrichpersistence.NewSeriesTextsRepository(db))
	// HTTP handler stays on bare `log` — see qbitLog godoc above.
	seriesTorrentsHandler := seriesdetailrest.NewSeriesTorrentsHandler(
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
		QbitCapacityLoop:      capacityLoop,
	}, nil
}

// InstanceBundle groups the instance-domain components constructed at boot.
// Returned by BuildInstance. Threaded into httpserver.NewServer (CRUDHandler,
// ProbeHandler) — the HTTP wirer remains in server.go for now.
//
// Field-level invariants:
//
//   - UC owns the WithWebhookReconciler + WithWebhookStatusCache chained
//     setters from the webhook bundle (story 335). The adapter is the
//     pre-baked WebhookBundle.ReconcilerAdapter — same pointer identity
//     as everywhere else.
//
//   - CRUDHandler wraps UC for the /api/v1/instances CRUD routes.
//
//   - ProbeHandler is the stateless POST /api/v1/instances/test handler;
//     it holds its own *http.Client (tuned for probe: 5s dial + TLS +
//     response-header timeouts, 64 KiB response-header cap, redirects
//     short-circuited with ErrUseLastResponse so probe assertions can
//     inspect the original status / Location).
//
//   - ProbeClient is exposed on the bundle for symmetry / tests; the
//     handler owns the only production reference.
type InstanceBundle struct {
	UC           *instance.UseCase
	CRUDHandler  *catalogrest.InstanceCRUDHandler
	ProbeHandler *catalogrest.InstanceProbeHandler
	ProbeClient  *http.Client
}

// BuildInstance wires the instance.UseCase + CRUD handler + Probe handler
// + probe HTTP client.
//
// Construction order mirrors the pre-336 inline body in server.go verbatim:
//
//  1. instance.New(instanceRepo, runtimeRepo, cipher, bus, log) chained
//     through WithWebhookReconciler(webhook.ReconcilerAdapter) +
//     WithWebhookStatusCache(webhook.StatusCache).
//  2. catalogrest.NewInstanceCRUDHandler(uc, log).
//  3. *http.Client tuned for probe (5s dial + TLS + response-header
//     timeouts, 64 KiB response-header cap, short-circuited redirects).
//  4. catalogrest.NewInstanceProbeHandler(probeClient, log).
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers.
func BuildInstance(
	persistence *PersistenceBundle,
	webhook *WebhookBundle,
	bus *runtime.Bus,
	log *slog.Logger,
) (*InstanceBundle, error) {
	// F-4b-8: instance CRUD UC is the operator-facing admin surface for
	// Sonarr instance management — operator-driven mutations belong to
	// the "admin" slot.
	adminLog := sharedports.DomainLogger(log, "admin")
	uc := instance.New(
		persistence.InstanceRepo,
		persistence.RuntimeRepo,
		persistence.Cipher,
		bus,
		adminLog,
	).
		WithWebhookReconciler(webhook.ReconcilerAdapter).
		WithWebhookStatusCache(webhook.StatusCache)

	crudHandler := catalogrest.NewInstanceCRUDHandler(uc, log)

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
	probeHandler := catalogrest.NewInstanceProbeHandler(probeClient, log)

	return &InstanceBundle{
		UC:           uc,
		CRUDHandler:  crudHandler,
		ProbeHandler: probeHandler,
		ProbeClient:  probeClient,
	}, nil
}
