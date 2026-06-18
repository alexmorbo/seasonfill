package wiring

import (
	"log/slog"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/ports"
)

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
	GrabRepo     *repositories.GrabRepository
	CooldownRepo *repositories.CooldownRepository
	OriginRepo   *repositories.OriginReleaseRepository
	DecisionRepo *repositories.DecisionRepository
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
	scanLog := ports.DomainLogger(log, "scan")

	scanRepo := repositories.NewScanRepository(db)
	decisionRepo := repositories.NewDecisionRepository(db)
	grabRepo := repositories.NewGrabRepository(db)
	cooldownRepo := repositories.NewCooldownRepository(db)
	originRepo := repositories.NewOriginReleaseRepository(db)

	txr := repositories.NewGormTransactor(db)
	evaluator := evaluate.NewPerInstanceUseCase(decisionRepo, log)
	grabUC := grab.NewUseCase(grabRepo, cooldownRepo, originRepo, sonarr.Classifier{}, log).
		WithTransactor(txr)

	// seriesCacheRepo is local to this wirer — see godoc above.
	seriesRepo := repositories.NewSeriesRepository(db)
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
