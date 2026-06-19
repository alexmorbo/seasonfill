package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/application/gc"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SchedulerEnrichmentJobs is the wiring-package boundary for the four
// enrichment-derived cron closures. The cmd/server EnrichmentBundle is
// in `package main` and cannot be imported by wiring; server.go fills
// this DTO from enrichBundle fields and hands it to BuildScheduler.
//
// Any field may be nil — BuildScheduler skips the corresponding
// Register call. UsesQuotaCounter is the DB-backed quota guard flag;
// when true the in-process OMDb budget reset cron is skipped because
// the DB guard rotates at UTC midnight implicitly.
type SchedulerEnrichmentJobs struct {
	Nightly          func(context.Context)
	OMDbBudgetReset  func(context.Context)
	OMDbDailyBatch   func(context.Context)
	UsesQuotaCounter bool
}

// SchedulerBundle groups the cron scheduler components constructed at
// boot. Returned by BuildScheduler.
//
// Factory is the SchedulerFactory captured by both the boot path and
// the reload SchedulerSubscriber. Story 301: closure captures the
// resolver's current location at construction time so PATCH'd timezone
// values take effect on the next rebuild.
//
// BootScheduler is the boot-time *scheduler.Scheduler with every cron
// job already Register()ed. It is nil when cfg.Cron.Enabled is false —
// the caller treats nil as "no cron". The caller is responsible for
// invoking BootScheduler.Start(rootCtx, scanUC), which is omitted from
// this wirer because Start needs rootCtx (owned by server.go) and
// scanUC's blocking semantics belong to the lifecycle ladder.
//
// Field-level invariants:
//
//   - All five cron jobs are Registered BEFORE the bundle is returned.
//     The scheduler's Register-before-Start contract is preserved.
//
//   - The weekly-gc job is Registered unconditionally when cron is
//     enabled; the scheduler decides whether to fire it based on
//     cfg.Cron.Enabled.
//
//   - The quota-counter-gc job is Registered unconditionally when cron
//     is enabled; it bounds external_service_quota_state at
//     #services × 7 rows regardless of the OMDb code path.
type SchedulerBundle struct {
	Factory       reload.SchedulerFactory
	BootScheduler *scheduler.Scheduler
}

// BuildScheduler builds the cron factory + boot scheduler and Registers
// every cron job. Mirrors the pre-341 inline body in server.go verbatim:
//
//  1. Construct schedulerFactory closure (captures tzResolver location).
//  2. If cfg.Cron.Enabled — build bootScheduler via factory.
//  3. If bootScheduler != nil && enrichmentJobs.Nightly != nil —
//     Register("enrichment-nightly", "0 4 * * *", Nightly).
//  4. If bootScheduler != nil:
//     - !UsesQuotaCounter && OMDbBudgetReset != nil —
//     Register("omdb-budget-reset", "0 4 * * *", OMDbBudgetReset).
//     - OMDbDailyBatch != nil —
//     Register("omdb-daily-batch", "30 4 * * *", OMDbDailyBatch).
//     - Register("quota-counter-gc", "15 4 * * *", quotaSweep) — closure
//     captures persistence.QuotaCounter + log.
//  5. If bootScheduler != nil — build weeklyJob (gc.WeeklyJob) over
//     locally constructed seriesRepo + liveAssetsRepo + mediaBundle.Store
//     + mediaBundle.AssetsRepo + persistence.DB, then
//     Register("weekly-gc", "0 5 * * 0", weeklyJob.Run).
//
// Inputs:
//   - persistence: DB, QuotaCounter (for the gc closure), TZResolver
//     (for the factory's location capture).
//   - mediaBundle: Store + AssetsRepo for the weekly media sweep.
//     A nil AssetsRepo or nil Store inside the bundle is supported —
//     gc.MediaSweepDeps handles nil gracefully.
//   - cfg: Cron.Enabled / Schedule / Jitter.
//   - enrichmentJobs: the four nil-OK closures + UsesQuotaCounter flag.
//   - log: shared logger.
//
// Returns: SchedulerBundle{Factory, BootScheduler}. BootScheduler is
// nil when cron is disabled; Factory is always non-nil because the
// reload subscriber needs it for future rebuilds.
//
// Errors: only Register can fail (duplicate name / invalid schedule),
// and the error is wrapped with the pre-341 message verbatim for parity.
func BuildScheduler(
	persistence *PersistenceBundle,
	mediaBundle *MediaBundle,
	cfg HTTPServeConfig,
	enrichmentJobs SchedulerEnrichmentJobs,
	log *slog.Logger,
) (*SchedulerBundle, error) {
	db := persistence.DB
	quotaCounter := persistence.QuotaCounter
	tzResolver := persistence.TZResolver

	// Story 301: closure factory captures the resolver's current
	// location at construction time. Built fresh on each scheduler
	// rebuild (boot + reload) so a pod restart picks up the
	// PATCH'd value. Already-running jobs do NOT pick up live
	// PATCHes — see story known_limitations.
	schedulerFactory := func(schedule string, jitter time.Duration, logger *slog.Logger) *scheduler.Scheduler {
		return scheduler.NewWithLocation(schedule, jitter, logger, tzResolver.Get())
	}
	var bootScheduler *scheduler.Scheduler
	if cfg.Cron.Enabled {
		bootScheduler = schedulerFactory(cfg.Cron.Schedule, cfg.Cron.Jitter, log)
	}

	// Register the nightly stale scan into the boot scheduler if cron
	// is enabled. Done BEFORE Start (now StartRegistered via the
	// legacy wrapper) so the registry is build-once.
	if bootScheduler != nil && enrichmentJobs.Nightly != nil {
		if err := bootScheduler.Register("enrichment-nightly", "0 4 * * *",
			enrichmentJobs.Nightly); err != nil {
			return nil, fmt.Errorf("register nightly enrichment: %w", err)
		}
	}

	// Story 213 (D-1) — OMDb daily batch + budget reset.
	// 04:00 — reset the in-process budget counter (must precede the
	// 04:30 batch so the batch runs against a fresh budget).
	// 04:30 — fan out library series with stale OMDb sync into the
	// enrichment dispatcher at PriorityCold.
	if bootScheduler != nil {
		// 305: in the DB-backed path the budget guard rotates at UTC
		// midnight implicitly — no explicit Reset needed. Only the
		// in-process fallback (no QuotaCounter) keeps the daily reset
		// cron, because its atomic counter must be Store(initial) at
		// midnight to refill.
		if !enrichmentJobs.UsesQuotaCounter && enrichmentJobs.OMDbBudgetReset != nil {
			if err := bootScheduler.Register("omdb-budget-reset", "0 4 * * *",
				enrichmentJobs.OMDbBudgetReset); err != nil {
				return nil, fmt.Errorf("register omdb budget reset: %w", err)
			}
		}
		if enrichmentJobs.OMDbDailyBatch != nil {
			if err := bootScheduler.Register("omdb-daily-batch", "30 4 * * *",
				enrichmentJobs.OMDbDailyBatch); err != nil {
				return nil, fmt.Errorf("register omdb daily batch: %w", err)
			}
		}
		// 305: daily GC sweep for the external_service_quota_state
		// table. Deletes windows older than 7 days so the table stays
		// bounded at #services × 7 rows at steady state. Runs at
		// 04:15 — between budget-reset (which is skipped in DB-mode)
		// and omdb-daily-batch (which runs at 04:30).
		if err := bootScheduler.Register("quota-counter-gc", "15 4 * * *",
			func(ctx context.Context) {
				cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
				deleted, err := quotaCounter.Reset(ctx, cutoff)
				if err != nil {
					log.WarnContext(ctx, "quota.counter.gc.failed",
						slog.String("error", err.Error()))
					return
				}
				log.InfoContext(ctx, "quota.counter.gc.swept",
					slog.Time("cutoff", cutoff),
					slog.Int64("deleted_rows", deleted))
			}); err != nil {
			return nil, fmt.Errorf("register quota-counter-gc: %w", err)
		}
	}

	// Story 218 (E-2) — weekly GC at Sunday 05:00. Best-effort
	// sub-tasks: orphan canon series sweep (90d grace) → media
	// asset sweep (30d cooldown vs live-hash set) → qbit event
	// prune (skipped until A-3 lands the table). Registered
	// unconditionally — the scheduler decides whether to fire it
	// based on cfg.Cron.Enabled.
	if bootScheduler != nil {
		// F-4b-8: scheduled garbage-collection sweep records — weekly
		// orchestrator + three sub-tasks all anchor on the new "gc" slot
		// in AllowedDomains. PRD §6.5.
		gcLog := sharedports.DomainLogger(log, "gc")
		seriesRepo := repositories.NewSeriesRepository(db)
		liveAssetsRepo := repositories.NewLiveAssetsRepository(db)
		weeklyJob := gc.WeeklyJob{
			OrphanSeries: gc.OrphanSeriesDeps{
				Repo:   seriesRepo,
				Logger: gcLog,
			}.Build(),
			MediaSweep: gc.MediaSweepDeps{
				LiveSet: liveAssetsRepo,
				Assets:  mediaBundle.AssetsRepo,
				Store:   mediaBundle.Store,
				Logger:  gcLog,
			}.Build(),
			EventPrune: gc.EventPruneDeps{
				DB:     db,
				Logger: gcLog,
			}.Build(),
			Logger: gcLog,
		}
		if err := bootScheduler.Register("weekly-gc", "0 5 * * 0", weeklyJob.Run); err != nil {
			return nil, fmt.Errorf("register weekly-gc: %w", err)
		}
	}

	return &SchedulerBundle{
		Factory:       reload.SchedulerFactory(schedulerFactory),
		BootScheduler: bootScheduler,
	}, nil
}
