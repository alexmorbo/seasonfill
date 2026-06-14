---
id: 337
title: Extract regrab + watchdog handlers wiring
status: done
phase: b-11-cmd-server-refactor
loc_estimate: 500
files_touched:
  - cmd/server/wiring/regrab.go
  - cmd/server/server.go
acceptance:
  - "go build + go test -race passes."
  - "wiring.BuildRegrab returns RegrabBundle{QbitSettingsUC, QbitSettingsHandler, BlacklistRepo, NoBetterCounterRepo, RegrabUC, RegrabLoop, WatchdogRollupHandler, WatchdogBlacklistHandler, WatchdogSeasonsHandler, WebhooksAggregateHandler, QbitLoader}."
  - "Reload bus subscriber still receives qbit settings updates."
depends_on: [332, 333, 334]
operator_attention: false
---

# Story 337 — Extract regrab + watchdog handlers wiring

## Scope

**IN:**
- `wiring/regrab.go` exporting `BuildRegrab(persistence, sonarrBundle, scanBundle, webhookBundle, log) (*RegrabBundle, error)`.
- Bundle covers: qbit settings UC + HTTP handler, BlacklistRepo, NoBetterCounterRepo, RegrabUC, RegrabLoop (constructor — `.Start(rootCtx)` stays in server.go), watchdog HTTP handlers (rollup, blacklist, seasons), WebhooksAggregateHandler, QbitLoader closure (already adapters-typed since story 328).

**OUT:**
- `loops.NewRegrabLoop(...).Start(rootCtx)` call stays in server.go (needs rootCtx).
- `startSubscribers(...)` keeps its current signature — bundle fields plug into the existing parameter list.

## Verification

```bash
go build ./...
go test ./... -race -count=1 -timeout 5m
```

## Notes

- Commit: `refactor(cmd/server): extract regrab wiring (B-11 step 15)`.

## §A — wiring/regrab.go (full source)

```go
package wiring

import (
	"context"
	"log/slog"

	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	infraregrab "github.com/alexmorbo/seasonfill/infrastructure/regrab"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/observability"
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
// bgWG is the process-wide background wait group — passed through to
// loops.NewRegrabLoop so the per-instance polling goroutines block
// graceful shutdown's drainBackground. The signature mirrors BuildScan.
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers.
func BuildRegrab(
	persistence *PersistenceBundle,
	sonarrBundle *SonarrBundle,
	scanBundle *ScanBundle,
	webhookBundle *WebhookBundle,
	bgWG interface {
		// bgWG is *sync.WaitGroup; declared inline-typed so this file
		// avoids the `sync` import (the field is only forwarded into
		// loops.NewRegrabLoop, which keeps the concrete pointer).
		// — see below for the real type.
	},
	log *slog.Logger,
) (*RegrabBundle, error) {
	return nil, nil // placeholder — real impl below
}
```

NOTE: the inline-typed bgWG above is rejected at compile time. We use `*sync.WaitGroup` and import `sync`. Final version below:

```go
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
)

// RegrabBundle — see godoc above.
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

	// Phase 10 Watchdog — settings CRUD.
	qbitSettingsRepo := repositories.NewQbitSettingsRepository(db)
	qbitSettingsUC := regrab.NewSettingsUseCase(qbitSettingsRepo, instanceRepo, cipher, log).
		WithWebhookChecker(adapters.NewWebhookChecker(sonarrBundle.InstanceReg))
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
		log,
	).WithMetrics(observability.WatchdogMetricsAdapter{}).
		WithDecisions(scanBundle.DecisionRepo)

	// RegrabLoop owns per-instance polling goroutines; SwapSettings is
	// called from the OnApplied fanout. NOT started here — server.go
	// owns rootCtx and calls .Start(rootCtx) inline after BuildRegrab
	// returns.
	regrabLoop := loops.NewRegrabLoop(regrabUC, observability.WatchdogMetricsAdapter{}, bgWG, log)

	// 047a — watchdog rollup handler.
	watchdogInstanceAdapter := adapters.NewWatchdogInstanceLister(instanceRepo, cipher)
	watchdogRollupHandler := handlers.NewWatchdogRollupHandler(
		qbitSettingsUC,            // SettingsLookup
		regrabUC,                  // RollupSnapshotProvider
		scanBundle.GrabRepo,       // rollupGrabCounter
		blacklistRepo,             // rollupBlacklistCounter
		watchdogInstanceAdapter,   // InstanceLister
		watchdogInstanceAdapter,   // InstanceIDLookup
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
```

## §B — server.go edits

### B-1: New import set

Drop unused imports after the extraction (some may stay due to other usages — verify):

- `infraregrab` — still used by torrentsync block (QbitClientFactoryFunc). KEEP.
- `observability` — still used by torrentsync `TorrentsyncMetricsAdapter`. KEEP.
- `handlers` — still used for many other handlers. KEEP.
- `repositories` — still used (seriesCacheRepo etc). KEEP.

### B-2: Insert BuildRegrab call after BuildInstance (line 285)

After `_ = instanceUC // reserved — see godoc` block, insert:

```go
	regrabBundle, err := wiring.BuildRegrab(persistence, sonarrBundle, scanBundle, webhookBundle, &bgWG, log)
	if err != nil {
		return nil, err
	}
	// Rebind locals for the remainder of New(). The bundle's fields
	// preserve the pre-337 names verbatim so every downstream call site
	// (httpserver.NewServer for the four watchdog handlers + qbit settings
	// handler + webhooks aggregate handler, torrentsyncFactory's
	// qbitSettingsUC lookup, startSubscribers for regrabLoop +
	// qbitLoader) keeps working unchanged.
	qbitSettingsUC := regrabBundle.QbitSettingsUC
	qbitSettingsHandler := regrabBundle.QbitSettingsHandler
	regrabLoopVal := regrabBundle.RegrabLoop
	watchdogRollupHandler := regrabBundle.WatchdogRollupHandler
	watchdogBlacklistHandler := regrabBundle.WatchdogBlacklistHandler
	watchdogSeasonsHandler := regrabBundle.WatchdogSeasonsHandler
	webhooksAggregateHandler := regrabBundle.WebhooksAggregateHandler
	qbitLoader := regrabBundle.QbitLoader

	// regrab loop owns per-instance polling goroutines; SwapSettings is
	// called from the OnApplied fanout below. The constructor is owned by
	// BuildRegrab; only the rootCtx-bearing .Start lives here.
	regrabLoopVal.Start(rootCtx)
```

### B-3: Delete lines 287-316 (qbitSettingsRepo + qbitSettingsUC + qbitSettingsHandler + regrab orchestrator block + regrabLoopVal.Start)

```go
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
		sonarrBundle.InstanceRegistry,
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
```

(replaced by B-2)

### B-4: Replace torrentsyncFactory's qbitSettingsUC reference

The torrentsync block currently references `qbitSettingsUC`. After B-2 that name still exists (rebound from bundle), so no change needed there.

### B-5: Delete lines 368-406 (047a + 047b + 098a + webhooks aggregate block)

```go
	// 047a — watchdog rollup handler wiring.
	watchdogInstanceAdapter := adapters.NewWatchdogInstanceLister(instanceRepo, cipher)
	watchdogRollupHandler := handlers.NewWatchdogRollupHandler(...)
	... etc through webhooksAggregateHandler.
```

(all of this is now produced by BuildRegrab — locals already rebound in B-2)

### B-6: Delete lines 408-444 (qbitLoader closure)

```go
	qbitLoader := adapters.QbitSettingsLoaderFunc(func(ctx context.Context) ...)
```

(replaced by B-2 rebind)

### B-7: Verify imports after edits

After the deletes, server.go no longer needs:
- `application/regrab` — KEEP (the regrab.Settings type is referenced indirectly via subscriber).
  - Check: is `regrab.X` named in surviving server.go body? `grep -n "regrab\." server.go` — used by startSubscribers / subscriber types via reload_wiring.go (different file). Audit shows server.go body uses only `regrab.NewUseCase` and `regrab.NewSettingsUseCase` directly — both move out. KEEP only if needed; the `regrab` package import may become unused. Remove if unused.
- `infraregrab` — torrentsync block still uses `infraregrab.QbitClientFactoryFunc{}` → KEEP.
- `observability` — torrentsync block still uses `observability.TorrentsyncMetricsAdapter{}` → KEEP.

Run `goimports` (or rely on `go build` to surface unused imports).

## Phase 2 — Apply commands

1. `Write cmd/server/wiring/regrab.go` (the final §A code).
2. `Edit cmd/server/server.go`:
   - B-2: insert BuildRegrab block.
   - B-3, B-5, B-6: delete original blocks.
   - B-7: remove unused imports if `go build` complains.
3. `gofmt -s -w cmd/server/ cmd/server/wiring/`
4. `go build ./...`
5. `go vet ./...`
6. `go test ./cmd/server/... -race -count=1 -timeout 5m`
7. `go test ./... -race -count=1 -timeout 10m`

## Phase 3 — Commit

```bash
git add -A cmd/server/
git status
git commit --author='AlexMorbo <alex@morbo.ru>' -m "refactor(cmd/server): extract regrab wiring (B-11 step 15)"
git log -1 --format='%h %s'
```

Flip story `status: ready` → `status: done`.
