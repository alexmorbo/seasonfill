package wiring

import (
	"context"
	"log/slog"
	"sync"

	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	infraregrab "github.com/alexmorbo/seasonfill/infrastructure/regrab"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/ports"
)

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
	NoBetterCounterRepo      *repositories.NoBetterCounterRepository
	RegrabUC                 *regrab.UseCase
	RegrabLoop               *loops.RegrabLoop
	WatchdogRollupHandler    *handlers.WatchdogRollupHandler
	WatchdogBlacklistHandler *handlers.WatchdogBlacklistHandler
	WatchdogSeasonsHandler   *handlers.WatchdogSeasonsHandler
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
	watchdogLog := ports.DomainLogger(log, "watchdog")

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
	noBetterCounterRepo := repositories.NewNoBetterCounterRepository(db)
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
	watchdogRollupHandler := handlers.NewWatchdogRollupHandler(
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
	seriesRepo := repositories.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	watchdogBlacklistHandler := handlers.NewWatchdogBlacklistHandler(
		blacklistRepo,           // BlacklistPager
		seriesCacheRepo,         // SeriesTitleResolver
		watchdogInstanceAdapter, // InstanceIDLookup
		log,
	)

	// 098a — watchdog seasons aggregate read view.
	watchdogSeasonsRepo := repositories.NewWatchdogSeasonsRepository(db)
	watchdogSeasonsHandler := handlers.NewWatchdogSeasonsHandler(
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
