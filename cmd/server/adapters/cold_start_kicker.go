// cold_start_kicker.go ships the B-9 Scope A boot-race fix
// (story 508). Background:
//
// At boot, BackfillSeries runs ~26ms after the dispatcher comes up. If
// sonarr_sync has NOT yet populated the series table (fresh deploy /
// empty DB / first install), ListMissingTMDBSync returns 0 → the
// resweep ticker waits 60s → another 60s → etc. By the time enrichment
// catches up, the operator has been staring at empty cards for 1-2
// minutes.
//
// ColdStartKicker arms itself once on the first BackfillSeries pass:
// if the pass scanned 0 ids → "kicker armed". The first subsequent
// scan_completed sweep (catalog scan UC) disarms + fires BackfillSeries
// IMMEDIATELY — enrichment now sees the freshly-synced rows and the
// dispatcher fans them out.
//
// Invariants:
//
//   - Trigger fires AT MOST ONCE per process lifetime. Multiple
//     sync_completions after the kick all no-op.
//   - Concurrent MarkPassResult + OnSyncCompleted calls are safe
//     (single mutex covers both).
//   - Trigger errors are logged at WARN; the kicker disarms BEFORE
//     calling trigger so a failing kick does not re-arm.
package adapters

import (
	"context"
	"log/slog"
	"sync"
)

// ColdStartKicker is the boot-race breaker. Construct once via
// NewColdStartKicker, register MarkPassResult on BackfillSeries
// boot pass + the OnSyncCompleted on scan.UseCase.WithPostScanCycle.
type ColdStartKicker struct {
	mu               sync.Mutex
	initialPassDone  bool
	initialPassEmpty bool
	fired            bool
	trigger          func(ctx context.Context) error
	log              *slog.Logger
}

// NewColdStartKicker wires the kicker. trigger + log MUST be non-nil;
// log must already carry a domain tag (production wiring passes
// sharedports.DomainLogger(log, "enrichment")).
func NewColdStartKicker(trigger func(ctx context.Context) error, log *slog.Logger) *ColdStartKicker {
	if trigger == nil {
		panic("cold start kicker: trigger required")
	}
	if log == nil {
		panic("cold start kicker: log required")
	}
	return &ColdStartKicker{
		trigger: trigger,
		log:     log,
	}
}

// MarkPassResult records the outcome of the boot BackfillSeries pass.
// Only the FIRST call is recorded — every later call is a no-op so the
// re-sweep ticker's recurring invocations do not re-arm the kicker
// after a successful run drained the series.
func (k *ColdStartKicker) MarkPassResult(seriesCount int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.initialPassDone {
		return
	}
	k.initialPassDone = true
	k.initialPassEmpty = seriesCount == 0
	k.log.Info("enrichment.cold_start_kicker.armed",
		slog.Bool("armed", k.initialPassEmpty),
		slog.Int("initial_series_count", seriesCount))
}

// OnSyncCompleted is the catalog scan UC hook (scan.UseCase.
// WithPostScanCycle). Fires once per scan_completed.
//
// Decision matrix:
//
//   - initialPassDone == false        → no-op (kicker not yet armed; boot path raced ahead of scan completion)
//   - initialPassEmpty == false       → no-op (boot pass found rows; cold-start was not racey)
//   - fired == true                   → no-op (kick already happened; subsequent scans hit normal PostSync path)
//   - else                            → DISARM + fire trigger
//
// The disarm-before-fire ordering means a concurrent OnSyncCompleted
// racing the trigger call cannot double-fire.
func (k *ColdStartKicker) OnSyncCompleted(ctx context.Context) {
	k.mu.Lock()
	shouldKick := k.initialPassDone && k.initialPassEmpty && !k.fired
	if shouldKick {
		k.fired = true
		k.initialPassEmpty = false // belt-and-braces; fired already gates
	}
	k.mu.Unlock()
	if !shouldKick {
		return
	}
	k.log.InfoContext(ctx, "enrichment.cold_start_kicker.firing",
		slog.String("trigger", "scan_completed_post_empty_boot_pass"))
	if err := k.trigger(ctx); err != nil {
		k.log.WarnContext(ctx, "enrichment.cold_start_kicker.kick_failed",
			slog.String("error", err.Error()))
		return
	}
	k.log.InfoContext(ctx, "enrichment.cold_start_kicker.kick_ok")
}
