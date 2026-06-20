package wiring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/gc"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/runtimeconfig"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/config"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
	watchdog "github.com/alexmorbo/seasonfill/internal/watchdog/infrastructure"
)

// HTTPServeConfig is the on-the-stack config DTO previously inlined as
// `httpServeConfig` in package main (story 323). It carries the subset
// of bootstrap + runtime fields the HTTP server, scheduler, and
// shutdown ladder read. Returned by BuildRuntimeConfig inside
// RuntimeConfigBundle.ServeConfig.
//
// All fields are read-only after construction. Server.cfg keeps a copy
// by value so Shutdown can read cfg.Scan.ShutdownGrace from the same
// value Run was constructed with.
type HTTPServeConfig struct {
	HTTP            config.HTTPConfig
	SonarrInstances []runtime.InstanceSnapshot
	DryRun          bool
	GlobalRateLimit runtime.RateLimitSnapshot
	Scan            runtime.ScanSnapshot
	Cron            runtime.CronSnapshot
}

// RuntimeConfigBundle is the output of BuildRuntimeConfig. It groups
// the boot-time snapshot, the application use case, the HTTP handler,
// and the assembled HTTPServeConfig — together the entire "runtime
// configuration" bounded context.
//
// Snap is the full runtime.Snapshot value loaded from the singleton
// row plus the instance list (sorted, defaults applied). Consumers in
// server.go read snap.GlobalRateLimit (for the initial limiter seed),
// snap.Auth (for downstream auth wiring), and pass snap to the reload
// subscribers' boot publish.
//
// UC is the runtimeconfig application use case, already configured
// with WithClientSecretEnv(bootCfg.Auth.OIDCClientSecret).
//
// Handler is the HTTP handler that delegates to UC.
//
// ServeConfig is the assembled HTTPServeConfig the HTTP server, the
// scheduler factory, and Shutdown all read.
type RuntimeConfigBundle struct {
	Snap        runtime.Snapshot
	UC          *runtimeconfig.UseCase
	Handler     *handlers.RuntimeConfigHandler
	ServeConfig HTTPServeConfig
}

// BuildRuntimeConfig seeds the runtime_config row on a fresh install,
// composes the boot snapshot from the row + instance list, and wires
// the runtimeconfig application use case + HTTP handler + the on-stack
// HTTPServeConfig DTO.
//
// The ctx parameter is reserved for future use. The current body uses
// a background context for the DB reads to mirror the pre-refactor
// behaviour in Server.New (the seed must complete even if the parent
// ctx already carries an outer-test-harness deadline). See the same
// note on BuildPersistence.
//
// Seed-on-empty: if runtimes.Get returns ports.ErrNotFound, the wirer
// upserts runtime.Defaults() and re-reads. Any other error from Get
// (or the upsert + reload pair) is wrapped and returned.
func BuildRuntimeConfig(
	ctx context.Context,
	persistence *PersistenceBundle,
	bootCfg *config.Bootstrap,
	bus *runtime.Bus,
	log *slog.Logger,
) (*RuntimeConfigBundle, error) {
	_ = ctx
	bgCtx := context.Background()

	// Seed runtime_config from Defaults() on a truly-fresh install.
	row, err := persistence.RuntimeRepo.Get(bgCtx)
	switch {
	case err == nil:
		// happy path
	case errors.Is(err, ports.ErrNotFound):
		if err := persistence.RuntimeRepo.Upsert(bgCtx, runtime.Defaults(), nil); err != nil {
			return nil, fmt.Errorf("seed runtime_config: %w", err)
		}
		row, err = persistence.RuntimeRepo.Get(bgCtx)
		if err != nil {
			return nil, fmt.Errorf("reload runtime_config after seed: %w", err)
		}
	default:
		return nil, fmt.Errorf("read runtime_config: %w", err)
	}

	instances, err := persistence.InstanceRepo.List(bgCtx, persistence.Cipher)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	for i := range instances {
		runtime.ApplyInstanceDefaults(&instances[i])
	}
	runtime.SortInstances(instances)

	snap := runtime.Snapshot{
		Cron: row.Cron, Scan: row.Scan, DryRun: row.DryRun,
		GlobalRateLimit: row.GlobalRateLimit, Auth: row.Auth,
		Instances: instances,
	}

	// F-4b-8: runtime-config UC backs the admin /settings PATCH endpoints
	// — operator-driven runtime mutations belong to the "admin" slot.
	adminLog := sharedports.DomainLogger(log, "admin")
	uc := runtimeconfig.New(persistence.RuntimeRepo, persistence.InstanceRepo,
		persistence.Cipher, bus, adminLog).
		WithClientSecretEnv(bootCfg.Auth.OIDCClientSecret)
	handler := handlers.NewRuntimeConfigHandler(uc, log)

	// cfg reads from snap (not bootstrap) for the runtime-mutable
	// fields. APIKey embedded into authCfg comes from MasterKey
	// derived in BuildPersistence — the HTTP auth layer compares
	// against the X-Api-Key header.
	authCfg := config.Auth{
		Enabled:          true,
		APIKey:           persistence.MasterKey,
		SessionTTL:       snap.Auth.SessionTTL,
		SecureCookie:     snap.Auth.SecureCookie,
		TrustedProxies:   snap.Auth.TrustedProxies,
		OIDCClientSecret: bootCfg.Auth.OIDCClientSecret,
	}
	httpCfg := bootCfg.HTTP
	httpCfg.Auth = authCfg
	serveCfg := HTTPServeConfig{
		HTTP:            httpCfg,
		SonarrInstances: instances,
		DryRun:          snap.DryRun,
		GlobalRateLimit: snap.GlobalRateLimit,
		Scan:            snap.Scan,
		Cron:            snap.Cron,
	}

	return &RuntimeConfigBundle{
		Snap:        snap,
		UC:          uc,
		Handler:     handler,
		ServeConfig: serveCfg,
	}, nil
}

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
		seriesRepo := enrichpersistence.NewSeriesRepository(db)
		liveAssetsRepo := enrichpersistence.NewLiveAssetsRepository(db)
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
				Repo:   repositories.NewQbitTorrentEventsRepository(db),
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

// subscriberReadyTimeout bounds how long StartSubscribers will wait
// for every subscriber to register its bus.Subscribe call. Defensive
// only — in normal operation each goroutine reaches Subscribe in
// microseconds. If we hit the timeout, the process is broken: main
// exits non-zero with a clear log line.
const subscriberReadyTimeout = 2 * time.Second

// sweepIntervalSetter is the narrow contract BuildOnAppliedFanout
// needs from the cooldown sweeper. Keeping it as an interface lets the
// fan-out be unit-tested without spinning up a real sweepLoop.
type sweepIntervalSetter interface {
	SetInterval(d time.Duration)
}

// regrabSwapper is the narrow contract BuildOnAppliedFanout needs from
// the regrab loop. Decoupled into an interface so the fanout test can
// stub it without spinning up a real per-instance goroutine.
type regrabSwapper interface {
	SwapSettings(map[string]regrab.Settings)
}

// torrentsyncSwapper is the narrow contract BuildOnAppliedFanout needs
// from the torrentsync loop. Identical SwapSettings shape as regrab
// — both loops consume the same qbit-settings projection from the
// reload-bus snapshot.
type torrentsyncSwapper interface {
	SwapSettings(map[string]regrab.Settings)
}

// qbitSettingsLoader is the narrow contract BuildOnAppliedFanout needs
// to project the qbit-settings repo into a map keyed by instance name.
// In production it's a closure over QbitSettingsRepository.List +
// instance lookup; in tests it's a fake that returns a fixed map.
type qbitSettingsLoader interface {
	Load(ctx context.Context) map[string]regrab.Settings
}

// BuildOnAppliedFanout wires the OnApplied hook that updates everything
// that depends on the freshly-rebuilt sonarr-client set: the scan UC
// instance list, the holder map HTTP handlers iterate, the health
// checker's registry membership + preflight client list, and the
// cooldown sweep cadence. Running ALL of that inside
// SonarrClientsSubscriber.apply (under its lock) closes the
// cross-subscriber race that would otherwise let one fan-out observer
// (e.g. the old HealthRegistrySubscriber) read a stale View().All()
// before the live set was rebuilt.
func BuildOnAppliedFanout(rootCtx context.Context, scanUC *scan.UseCase, holder *adapters.InstanceMapHolder, checker reload.HealthChecker, wd *watchdog.Watchdog, sweeper sweepIntervalSetter, regrabLoop regrabSwapper, torrentsyncLoop torrentsyncSwapper, qbitLoader qbitSettingsLoader, log *slog.Logger) reload.OnAppliedFunc {
	return func(snap runtime.Snapshot, clients map[string]ports.SonarrClient) {
		nextSlice := make([]scan.Instance, 0, len(snap.Instances))
		nextMap := make(map[string]scan.Instance, len(snap.Instances))
		clientSlice := make([]ports.SonarrClient, 0, len(snap.Instances))
		names := make([]string, 0, len(snap.Instances))
		cfgByName := make(map[string]config.HealthCheckConfig, len(snap.Instances))
		for _, inst := range snap.Instances {
			client, ok := clients[inst.Name]
			if !ok || client == nil {
				// Should be impossible: clients is built by the same
				// apply iterating the same snap.Instances. Log and
				// skip rather than mishandle.
				log.Warn("onApplied fanout: client missing for instance",
					slog.String("instance", inst.Name))
				continue
			}
			si := scan.Instance{Config: inst, Client: client}
			nextSlice = append(nextSlice, si)
			nextMap[inst.Name] = si
			clientSlice = append(clientSlice, client)
			names = append(names, inst.Name)
			cfgByName[inst.Name] = config.NewHealthCheckConfig(inst.HealthCheck)
		}
		scanUC.SwapInstances(nextSlice)
		holder.Replace(nextMap)
		checker.ReplaceClients(clientSlice, names)
		wd.SwapConfigs(cfgByName)
		if sweeper != nil {
			sweeper.SetInterval(snap.Scan.CooldownSweep)
		}
		scanUC.SwapDryRun(snap.DryRun)

		// Phase 10 — Watchdog regrab loop fanout. Loaded fresh from the
		// repo on every publish so the loop sees the latest qBit settings
		// without a separate subscriber. The lookup runs under the
		// SonarrClientsSubscriber lock (we're inside its fanout closure)
		// so concurrent SwapSettings calls cannot interleave.
		//
		// Story 220 (A-2) — torrentsync loop consumes the same projection;
		// we reuse the qbitMap we already fetched rather than calling
		// qbitLoader.Load a second time per publish.
		if (regrabLoop != nil || torrentsyncLoop != nil) && qbitLoader != nil {
			qbitMap := qbitLoader.Load(rootCtx)
			if regrabLoop != nil {
				regrabLoop.SwapSettings(qbitMap)
			}
			if torrentsyncLoop != nil {
				torrentsyncLoop.SwapSettings(qbitMap)
			}
		}

		go checker.Preflight(rootCtx)
	}
}

// SubscriberDeps groups the cross-bundle inputs StartSubscribers needs
// that are not themselves bundles. Kept as a struct so the signature
// stays readable even as new subscribers join over time.
type SubscriberDeps struct {
	// Snap is the boot snapshot — its GlobalRateLimit field seeds the
	// GlobalRateLimiterSubscriber.
	Snap runtime.Snapshot
	// Engine is the gin engine the AuthMiddlewareSubscriber rewires on
	// each apply. Read from the http server: httpServer.Engine().
	Engine *gin.Engine
	// AuthRuntimePtr is the live AuthRuntime pointer the
	// AuthMiddlewareSubscriber atomically swaps. Read from the http
	// server: httpServer.AuthHandler().AuthRuntime() (nil-OK when auth
	// disabled).
	AuthRuntimePtr *middleware.AuthRuntimePointer
	// ClientSecretEnv is the OIDC client_secret env override forwarded to
	// the AuthMiddlewareSubscriber. From bootCfg.Auth.OIDCClientSecret.
	ClientSecretEnv string
}

// StartSubscribers launches every reload subscriber under bgWG and
// blocks until each has registered on the bus, then returns. ctx is
// the long-lived rootCtx — SIGTERM cancels it and every subscriber
// drains. Returns the SchedulerSubscriber + SonarrClientsSubscriber
// so server.go can hand them off to shutdown and to the testcontext
// hook (notifyTestContext).
//
// If any subscriber fails to register within subscriberReadyTimeout
// the function returns an error and the caller is expected to exit
// non-zero. In normal operation this returns within microseconds.
//
// The function takes wired bundles instead of the pre-344 raw-deps
// list. Each bundle field consumed below preserves its pre-344 name
// verbatim so the behavior is byte-equivalent — only the
// argument-construction site (server.go) changed.
func StartSubscribers(
	ctx context.Context,
	bgWG *sync.WaitGroup,
	bus *runtime.Bus,
	persistence *PersistenceBundle,
	sonarr *SonarrBundle,
	scan *ScanBundle,
	watchdogBundle *WatchdogBundle,
	regrab *RegrabBundle,
	torrentsync *TorrentsyncBundle,
	scheduler *SchedulerBundle,
	deps SubscriberDeps,
	log *slog.Logger,
) (*reload.SchedulerSubscriber, *reload.SonarrClientsSubscriber, error) {
	subSched := reload.NewSchedulerSubscriber(ctx, scheduler.BootScheduler, scan.ScanUC,
		scheduler.Factory, log)
	subClients := reload.NewSonarrClientsSubscriber(sonarr.ClientsByName, sonarr.ClientFactory, log).
		WithWaitGroup(bgWG).
		WithOnApplied(BuildOnAppliedFanout(
			ctx,
			scan.ScanUC,
			sonarr.Holder,
			watchdogBundle.Checker,
			watchdogBundle.Watchdog,
			scan.Sweeper,
			regrab.RegrabLoop,
			torrentsync.Loop,
			regrab.QbitLoader,
			log,
		))

	subRate := reload.NewGlobalRateLimiterSubscriber(sonarr.GlobalLimiterPtr,
		reload.DefaultGlobalLimiterFactory, deps.Snap.GlobalRateLimit, log)
	subAuth := reload.NewAuthMiddlewareSubscriber(deps.AuthRuntimePtr, deps.Engine, log,
		persistence.RuntimeRepo, deps.ClientSecretEnv)
	// Note: NewAuthMiddlewareSubscriber positional order is
	// (ptr, engine, logger, runtimeRepo, clientSecretEnv) per
	// infrastructure/reload/auth_middleware_subscriber.go — preserved
	// verbatim from the pre-344 call site.

	runners := []func(context.Context, *runtime.Bus, func()){
		subSched.Run, subClients.Run, subRate.Run, subAuth.Run,
	}
	names := []string{"scheduler", "sonarrClients", "globalRateLimiter", "authMiddleware"}

	ready := make([]chan struct{}, len(runners))
	for i := range ready {
		ready[i] = make(chan struct{})
	}

	for i, run := range runners {
		bgWG.Add(1)
		runFn := run
		readyCh := ready[i]
		go func() {
			defer bgWG.Done()
			runFn(ctx, bus, func() { close(readyCh) })
		}()
	}

	if err := waitAllReady(ready, subscriberReadyTimeout, names, log); err != nil {
		return nil, nil, err
	}

	return subSched, subClients, nil
}

// waitAllReady blocks until every channel in `ready` is closed, or
// until `timeout` elapses. On timeout it returns an error naming the
// subscribers that failed to register; their goroutines remain
// running (they'll get cleaned up when ctx is cancelled) but main
// is expected to log + exit.
func waitAllReady(ready []chan struct{}, timeout time.Duration, names []string, log *slog.Logger) error {
	allReady := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Add(len(ready))
		for _, r := range ready {
			go func() {
				defer wg.Done()
				<-r
			}()
		}
		wg.Wait()
		close(allReady)
	}()

	select {
	case <-allReady:
		return nil
	case <-time.After(timeout):
		var stuck []string
		for i, r := range ready {
			select {
			case <-r:
			default:
				stuck = append(stuck, names[i])
			}
		}
		log.Error("StartSubscribers: timeout waiting for subscribers to register",
			slog.Duration("timeout", timeout),
			slog.Any("stuck", stuck))
		return fmt.Errorf("StartSubscribers: timeout waiting for subscribers to register: %v", stuck)
	}
}
