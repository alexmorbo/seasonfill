package scan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/errtext"
	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
	grab "github.com/alexmorbo/seasonfill/internal/grab/app"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/observability"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

var (
	ErrScanAlreadyRunning = errors.New("scan already running for instance")
	ErrUnknownInstance    = errors.New("unknown instance")
	// ErrScanNotRunning: Cancel called on a scan_id not in inflight —
	// either unknown or already terminal. Caller's 404 either way.
	ErrScanNotRunning = errors.New("scan not running")
)

type Trigger string

const (
	TriggerCron    Trigger = "cron"
	TriggerManual  Trigger = "manual"
	TriggerStartup Trigger = "startup"
	TriggerRescan  Trigger = "rescan"
)

// InflightController is the narrow surface the rescan use case needs to
// hook into the scan use case's per-instance single-flight lock and
// background WaitGroup. *UseCase satisfies this — see the compile-time
// assertion below.
type InflightController interface {
	AcquireInstance(name shareddomain.InstanceName, scanID uuid.UUID) error
	ReleaseInstance(name shareddomain.InstanceName)
	SetInflightCancel(name shareddomain.InstanceName, cancel context.CancelFunc)
	BackgroundWG() *sync.WaitGroup
}

var _ InflightController = (*UseCase)(nil)

// AcquireInstance reserves the per-instance scan slot for scanID.
// Returns ErrScanAlreadyRunning if the instance is busy.
func (u *UseCase) AcquireInstance(name shareddomain.InstanceName, scanID uuid.UUID) error {
	return u.acquire(name, scanID)
}

// ReleaseInstance frees the per-instance scan slot. Safe to call when
// no slot is held (no-op).
func (u *UseCase) ReleaseInstance(name shareddomain.InstanceName) { u.release(name) }

// SetInflightCancel swaps the no-op CancelFunc seeded by AcquireInstance
// for the real one held by the goroutine, so /scans/:id/cancel can
// signal an in-flight rescan goroutine the same way it signals a scan.
func (u *UseCase) SetInflightCancel(name shareddomain.InstanceName, cancel context.CancelFunc) {
	u.setInflightCancel(name, cancel)
}

// BackgroundWG returns the process-wide drain WaitGroup (nil if not
// wired). The rescan use case Add(1)s on goroutine spawn and Done()s on
// exit so SIGTERM-drain blocks on outstanding rescans too.
func (u *UseCase) BackgroundWG() *sync.WaitGroup { return u.bgWG }

// Barrier is an optional synchronization hook used by tests to make
// runOne deterministically overlap two goroutines. nil in production.
type Barrier interface {
	Reached(instance shareddomain.InstanceName)
}

type Instance struct {
	Config config.SonarrInstance
	Client ports.SonarrClient
}

// inflightEntry carries everything Cancel needs to reach a running
// goroutine: row id + the CancelFunc of the detached ctx. CancelFunc is
// idempotent per Go context contract; no sync.Once needed.
type inflightEntry struct {
	ID     uuid.UUID
	Cancel context.CancelFunc
}

// HealthRegistry is the (small) subset of instance.Registry the scan loop
// needs. Keeping it as an interface lets tests inject a fake without pulling
// the real registry's listener machinery.
type HealthRegistry interface {
	Get(name string) (instance.Snapshot, bool)
	MarkUnavailable(name string, state instance.Health, lastErr string, at time.Time) (instance.Health, bool)
}

type UseCase struct {
	instances   atomic.Pointer[[]Instance]
	evaluator   *evaluate.UseCase
	scans       ports.ScanRepository
	grabUC      *grab.UseCase
	cooldowns   ports.CooldownRepository
	origins     ports.OriginReleaseRepository
	seriesCache ports.SeriesCacheRepository
	seasonStats SeasonStatsRepository
	health      HealthRegistry
	logger      *slog.Logger
	dryRun      atomic.Bool

	mu       sync.Mutex
	inflight map[shareddomain.InstanceName]inflightEntry

	barrier Barrier

	// bgWG (optional) is incremented before spawning an async scan
	// goroutine and decremented when it exits. cmd/server wires the
	// process-wide WaitGroup here so SIGTERM-drain blocks on
	// outstanding scans. nil in tests that don't care about drain.
	bgWG *sync.WaitGroup
}

func NewUseCase(
	instances []Instance,
	evaluator *evaluate.UseCase,
	scans ports.ScanRepository,
	logger *slog.Logger,
	dryRun bool,
) *UseCase {
	uc := &UseCase{
		evaluator: evaluator,
		scans:     scans,
		logger:    logger,
		inflight:  make(map[shareddomain.InstanceName]inflightEntry),
	}
	uc.dryRun.Store(dryRun)
	cp := append([]Instance(nil), instances...)
	uc.instances.Store(&cp)
	return uc
}

// loadInstances returns the live []Instance set by either the
// constructor or the last SwapInstances call.
func (u *UseCase) loadInstances() []Instance {
	p := u.instances.Load()
	if p == nil {
		return nil
	}
	return *p
}

// SwapInstances atomically replaces the instances slice. Called
// from the scanInstances reload subscriber. The slice is copied so
// the caller cannot mutate it after the swap.
func (u *UseCase) SwapInstances(next []Instance) {
	cp := append([]Instance(nil), next...)
	u.instances.Store(&cp)
}

// SwapDryRun atomically replaces the global dry-run default used by
// instanceDryRun when neither a per-call override nor a per-instance
// Config.DryRun is set. Called from buildOnAppliedFanout on every
// reload publish so toggling dry_run via the runtime config UI takes
// effect without a process restart.
func (u *UseCase) SwapDryRun(b bool) {
	u.dryRun.Store(b)
}

func (u *UseCase) WithGrabUseCase(g *grab.UseCase) *UseCase             { u.grabUC = g; return u }
func (u *UseCase) WithCooldowns(c ports.CooldownRepository) *UseCase    { u.cooldowns = c; return u }
func (u *UseCase) WithOrigins(o ports.OriginReleaseRepository) *UseCase { u.origins = o; return u }
func (u *UseCase) WithSeriesCache(c ports.SeriesCacheRepository) *UseCase {
	u.seriesCache = c
	return u
}

// WithSeasonStats wires the per-(instance, sonarr_series_id, season_number)
// stats writer used by fillSeriesCache. Story 380: without this, the scan
// loop never populated season_stats so SeriesSeasonsAccordion rendered
// 0/N for every season the scan_skip_handled_seasons fast-path covered.
// Nil-OK — fillSeriesCache no-ops when unset (same pattern as seriesCache).
func (u *UseCase) WithSeasonStats(s SeasonStatsRepository) *UseCase { u.seasonStats = s; return u }
func (u *UseCase) WithHealthRegistry(r HealthRegistry) *UseCase     { u.health = r; return u }
func (u *UseCase) WithBarrier(b Barrier) *UseCase                   { u.barrier = b; return u }

// WithWaitGroup wires the process-wide background wait group so
// async scan goroutines block graceful shutdown's drainBackground.
func (u *UseCase) WithWaitGroup(wg *sync.WaitGroup) *UseCase { u.bgWG = wg; return u }

type RunResult struct {
	ScanRunID    uuid.UUID
	InstanceName shareddomain.InstanceName
	Status       string
	Started      time.Time
	Finished     time.Time
	Series       int
	Candidates   int
	Grabs        int
	GrabsFailed  int
	Errors       int
}

func (u *UseCase) IsAnyRunning() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.inflight) > 0
}

func (u *UseCase) InflightScans() map[string]uuid.UUID {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]uuid.UUID, len(u.inflight))
	for k, v := range u.inflight {
		out[string(k)] = v.ID
	}
	return out
}

func (u *UseCase) Run(parent context.Context, trigger Trigger) ([]RunResult, error) {
	// D43: cron MUST skip manual-mode instances. UI/API triggers
	// (TriggerManual/Startup) ignore mode — `RunInstance` is the
	// supported manual path.
	instances := u.loadInstances()
	eligible := make([]int, 0, len(instances))
	for i := range instances {
		if trigger == TriggerCron && instances[i].Config.Mode == "manual" {
			u.logger.InfoContext(parent, "cron_skipped_manual_instance",
				slog.String("instance", instances[i].Config.Name))
			continue
		}
		eligible = append(eligible, i)
	}

	results := make([]RunResult, len(eligible))
	errs := make([]error, len(eligible))

	var wg sync.WaitGroup
	for outIdx, srcIdx := range eligible {
		wg.Add(1)
		go func(out, src int) {
			defer wg.Done()
			res, err := u.runOne(parent, instances[src], trigger, nil)
			results[out] = res
			errs[out] = err
		}(outIdx, srcIdx)
	}
	wg.Wait()
	return results, errors.Join(errs...)
}

// RunInstance triggers a scan on the named instance, ignoring `mode`
// (manual instances are reachable here on purpose — UI/API path).
// `seriesIDs` narrows ListSeries before evaluate; nil/empty = scan
// every series. Unknown IDs are dropped with a WARN (Q-010-3).
func (u *UseCase) RunInstance(parent context.Context, name string, trigger Trigger, seriesIDs ...shareddomain.SonarrSeriesID) (RunResult, error) {
	for _, inst := range u.loadInstances() {
		if inst.Config.Name == name {
			return u.runOne(parent, inst, trigger, seriesIDs)
		}
	}
	return RunResult{}, fmt.Errorf("%w: %s", ErrUnknownInstance, name)
}

// StartInstance schedules a scan on the named instance and returns
// immediately with status="running". See StartInstanceWithDryRun for
// the full contract; this wrapper preserves the original signature so
// existing test call sites stay unchanged.
func (u *UseCase) StartInstance(parent context.Context, name string, trigger Trigger, seriesIDs ...shareddomain.SonarrSeriesID) (RunResult, error) {
	return u.StartInstanceWithDryRun(parent, name, trigger, nil, seriesIDs...)
}

// StartInstanceWithDryRun is the trigger-an-instance entry point that
// honours an optional per-request dry-run override. `dryRunOverride`
// has precedence over the per-instance setting and the global default:
//   - nil  -> use the per-instance DryRun (if set), else global.
//   - &true  -> force dry run.
//   - &false -> force real grab even if the instance is configured dry.
//
// Errors are synchronous-only (validation + lock + Create); once those
// pass, the returned RunResult carries Status="running" and the
// goroutine reports terminal status through ScanRecord updates only.
func (u *UseCase) StartInstanceWithDryRun(parent context.Context, name string, trigger Trigger, dryRunOverride *bool, seriesIDs ...shareddomain.SonarrSeriesID) (RunResult, error) {
	instances := u.loadInstances()
	var found *Instance
	for i := range instances {
		if instances[i].Config.Name == name {
			found = &instances[i]
			break
		}
	}
	if found == nil {
		return RunResult{}, fmt.Errorf("%w: %s", ErrUnknownInstance, name)
	}
	return u.startOne(parent, *found, trigger, seriesIDs, dryRunOverride)
}

// Start schedules scans on every eligible instance (cron-mode rules
// from D43 apply: TriggerCron skips manual instances). Returns one
// RunResult per spawned scan, each with Status="running". Failures
// from the prelude (lock contention, ScanRecord create) appear in
// the joined error and have a non-running Status in their RunResult.
// Cron / startup never override dry-run — the override is a UI-only
// affordance.
func (u *UseCase) Start(parent context.Context, trigger Trigger) ([]RunResult, error) {
	instances := u.loadInstances()
	eligible := make([]int, 0, len(instances))
	for i := range instances {
		if trigger == TriggerCron && instances[i].Config.Mode == "manual" {
			u.logger.InfoContext(parent, "cron_skipped_manual_instance",
				slog.String("instance", instances[i].Config.Name))
			continue
		}
		eligible = append(eligible, i)
	}
	results := make([]RunResult, 0, len(eligible))
	errs := make([]error, 0, len(eligible))
	for _, idx := range eligible {
		res, err := u.startOne(parent, instances[idx], trigger, nil, nil)
		results = append(results, res)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return results, errors.Join(errs...)
}

// startOne owns the synchronous prelude. Returns RunResult with
// ScanRunID set and Status="running" as soon as the goroutine is
// launched. The goroutine itself calls runDetached and updates
// ScanRecord on completion. `dryRunOverride` — when non-nil —
// overrides both the per-instance DryRun and the global default.
func (u *UseCase) startOne(parent context.Context, inst Instance, trigger Trigger, seriesIDs []shareddomain.SonarrSeriesID, dryRunOverride *bool) (RunResult, error) {
	scanID := uuid.New()
	instName := shareddomain.InstanceName(inst.Config.Name)

	if u.health != nil {
		if snap, ok := u.health.Get(inst.Config.Name); ok && !snap.Health.IsAvailable() {
			u.logger.InfoContext(parent, "instance_unavailable, skipping scan",
				slog.String("instance", inst.Config.Name),
				slog.String("state", string(snap.Health)),
			)
			return RunResult{InstanceName: instName, Status: "skipped"},
				fmt.Errorf("%w: %s state=%s",
					domain.ErrInstanceUnavailable, inst.Config.Name, snap.Health)
		}
	}

	if err := u.acquire(instName, scanID); err != nil {
		return RunResult{InstanceName: instName, Status: "conflict"}, err
	}

	dryRun := u.instanceDryRun(inst, dryRunOverride)
	started := time.Now().UTC()
	rec := ports.ScanRecord{
		ID:           scanID,
		InstanceName: instName,
		Trigger:      string(trigger),
		StartedAt:    started,
		Status:       "running",
		DryRun:       dryRun,
	}
	createCtx := logger.WithTraceID(context.Background(), scanID.String())
	if err := u.scans.Create(createCtx, rec); err != nil {
		u.release(instName)
		u.logger.ErrorContext(parent, "create scan record failed",
			slog.String("instance", inst.Config.Name),
			slog.String("error", err.Error()))
		return RunResult{ScanRunID: scanID, InstanceName: instName, Status: "failed"}, err
	}

	u.logger.InfoContext(createCtx, "scan_started",
		slog.String("instance", inst.Config.Name),
		slog.String("trigger", string(trigger)),
		slog.Bool("dry_run", dryRun),
		slog.Bool("dry_run_override", dryRunOverride != nil),
		slog.Bool("async", true),
	)

	ctx, cancel := context.WithCancel(logger.WithTraceID(context.Background(), scanID.String()))
	u.setInflightCancel(instName, cancel)
	if u.bgWG != nil {
		u.bgWG.Add(1)
	}
	go func(inst Instance, rec ports.ScanRecord, seriesIDs []shareddomain.SonarrSeriesID, started time.Time) {
		if u.bgWG != nil {
			defer u.bgWG.Done()
		}
		defer cancel()
		defer u.release(shareddomain.InstanceName(inst.Config.Name))
		u.runDetached(ctx, inst, rec, trigger, seriesIDs, started, dryRun)
	}(inst, rec, seriesIDs, started)

	return RunResult{
		ScanRunID:    scanID,
		InstanceName: instName,
		Status:       "running",
		Started:      started,
	}, nil
}

// instanceDryRun decides the effective dry-run flag for one instance.
// Precedence: request override > instance config > global default.
// `override` non-nil wins outright (this is the per-scan UI knob from
// POST /scan); otherwise the existing D-2.6 rule applies — per-instance
// *bool wins, then the global default.
func (u *UseCase) instanceDryRun(inst Instance, override *bool) bool {
	if override != nil {
		return *override
	}
	if inst.Config.DryRun != nil {
		return *inst.Config.DryRun
	}
	return u.dryRun.Load()
}

func (u *UseCase) runOne(parent context.Context, inst Instance, trigger Trigger, seriesIDs []shareddomain.SonarrSeriesID) (RunResult, error) {
	scanID := uuid.New()
	ctx := logger.WithTraceID(parent, scanID.String())
	instName := shareddomain.InstanceName(inst.Config.Name)

	// Pre-scan health gate (D-2.3). Skip when the registry says Unavailable*.
	if u.health != nil {
		if snap, ok := u.health.Get(inst.Config.Name); ok && !snap.Health.IsAvailable() {
			u.logger.InfoContext(ctx, "instance_unavailable, skipping scan",
				slog.String("instance", inst.Config.Name),
				slog.String("state", string(snap.Health)),
			)
			return RunResult{InstanceName: instName, Status: "skipped"},
				fmt.Errorf("%w: %s state=%s",
					domain.ErrInstanceUnavailable, inst.Config.Name, snap.Health)
		}
	}
	if err := u.acquire(instName, scanID); err != nil {
		return RunResult{InstanceName: instName, Status: "conflict"}, err
	}
	defer u.release(instName)

	if u.barrier != nil {
		u.barrier.Reached(instName)
	}

	dryRun := u.instanceDryRun(inst, nil)
	started := time.Now().UTC()
	rec := ports.ScanRecord{
		ID: scanID, InstanceName: instName,
		Trigger: string(trigger), StartedAt: started,
		Status: "running", DryRun: dryRun,
	}
	if err := u.scans.Create(ctx, rec); err != nil {
		u.logger.ErrorContext(ctx, "create scan record failed", slog.String("error", err.Error()))
		return RunResult{ScanRunID: scanID, InstanceName: instName, Status: "failed"}, err
	}
	u.logger.InfoContext(ctx, "scan_started",
		slog.String("instance", inst.Config.Name),
		slog.String("trigger", string(trigger)),
		slog.Bool("dry_run", dryRun),
	)

	return u.processScan(ctx, inst, rec, trigger, seriesIDs, started, dryRun)
}

// runDetached is the goroutine body for the async path. Prelude (lock,
// Create) ran in startOne; this function trusts the lock + record already
// exist and dives straight into processScan. The barrier hook runs here so
// existing concurrency tests can still observe entry from an async path.
func (u *UseCase) runDetached(ctx context.Context, inst Instance, rec ports.ScanRecord, trigger Trigger, seriesIDs []shareddomain.SonarrSeriesID, started time.Time, dryRun bool) {
	if u.barrier != nil {
		u.barrier.Reached(shareddomain.InstanceName(inst.Config.Name))
	}
	if _, err := u.processScan(ctx, inst, rec, trigger, seriesIDs, started, dryRun); err != nil {
		// Terminal state is already persisted by finalize*; logging here is
		// just so an operator tailing slog sees the cause without GETing the
		// scan record.
		u.logger.WarnContext(ctx, "async_scan_terminated_with_error",
			slog.String("instance", inst.Config.Name),
			slog.String("error", err.Error()))
	}
}

func (u *UseCase) processScan(ctx context.Context, inst Instance, rec ports.ScanRecord, trigger Trigger, seriesIDs []shareddomain.SonarrSeriesID, started time.Time, dryRun bool) (RunResult, error) {
	instName := shareddomain.InstanceName(inst.Config.Name)
	observability.IncActiveScans(instName)
	defer observability.DecActiveScans(instName)
	scanID := rec.ID // 011c: was `scanID := uuid.New()` in old runOne — now reuse rec.ID

	seriesList, err := inst.Client.ListSeries(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrInstanceUnauthorized) {
			return u.finalizeScanAborted(ctx, rec, inst, started, err)
		}
		return u.finalizeScanFailed(ctx, rec, inst, started, fmt.Errorf("list series: %w", err))
	}

	u.fillSeriesCache(ctx, inst, seriesList)

	if len(seriesIDs) > 0 {
		// Q-010-3: stale UI cache may reference IDs not in this
		// instance. Skip-with-warn so partial scans still happen.
		want := make(map[shareddomain.SonarrSeriesID]struct{}, len(seriesIDs))
		for _, id := range seriesIDs {
			want[id] = struct{}{}
		}
		filtered := make([]series.Series, 0, len(want))
		matched := make(map[shareddomain.SonarrSeriesID]struct{}, len(want))
		for _, s := range seriesList {
			if _, ok := want[s.ID]; ok {
				filtered = append(filtered, s)
				matched[s.ID] = struct{}{}
			}
		}
		if len(matched) < len(want) {
			skipped := make([]int, 0, len(want)-len(matched))
			for id := range want {
				if _, ok := matched[id]; !ok {
					skipped = append(skipped, int(id))
				}
			}
			sort.Ints(skipped)
			u.logger.WarnContext(ctx, "scan_series_ids_unknown_skipped",
				slog.String("instance", inst.Config.Name),
				slog.Int("requested", len(want)),
				slog.Int("matched", len(matched)),
				slog.Any("skipped_series_ids", skipped))
		}
		seriesList = filtered
	}

	tagFilter, tagErr := buildTagFilter(ctx, inst)
	if tagErr != nil {
		// Auth abort wins over D-2.5 fail-CLOSED — a 401/403 means the
		// instance is unreachable, so no useful work is possible. The
		// fail-CLOSED gate below is for transient ListTags failures only.
		if errors.Is(tagErr, domain.ErrInstanceUnauthorized) {
			return u.finalizeScanAborted(ctx, rec, inst, started, tagErr)
		}
		// D-2.5 / M-new-2: fail-CLOSED when include is non-empty.
		if len(inst.Config.Tags.Include) > 0 {
			u.logger.ErrorContext(ctx, "tag filter failed, aborting scan (fail-closed)",
				slog.String("instance", inst.Config.Name),
				slog.String("error", tagErr.Error()),
			)
			return u.finalizeScanFailed(ctx, rec, inst, started, fmt.Errorf("tag filter: %w", tagErr))
		}
		u.logger.WarnContext(ctx, "tag filter failed (fail-open, include empty)",
			slog.String("instance", inst.Config.Name),
			slog.String("error", tagErr.Error()),
		)
	}

	processed := 0
	candidates := 0
	grabsDone := 0
	grabsFailed := 0
	consecutiveGrabFails := 0
	errorsCount := 0
	pendingDelta := 0 // 011c: batched series_scanned delta (Q-011-5)

	maxGrabs := inst.Config.Limits.MaxGrabsPerScan
	if maxGrabs <= 0 {
		maxGrabs = 10
	}
	maxSeries := inst.Config.Limits.ScanMaxSeries
	if maxSeries <= 0 {
		maxSeries = len(seriesList)
	}

	profileCache := make(map[int]ports.QualityProfile)

	for _, s := range seriesList {
		if processed >= maxSeries {
			break
		}
		if err := ctx.Err(); err != nil {
			break
		}

		if reason, ok := tagFilter.skip(s.TagIDs); ok {
			u.logger.DebugContext(ctx, "series skipped by tag filter",
				slog.Int("series_id", int(s.ID)),
				slog.String("title", s.Title),
				slog.String("reason", reason),
			)
			continue
		}

		seasons := s.MonitoredSeasons()
		if len(seasons) == 0 {
			continue
		}
		processed++
		pendingDelta++ // 011c: count series committed to evaluation

		// Series-level fast-path: if EVERY monitored season is already
		// complete (Aired - Existing <= 0), no Sonarr work is possible
		// — emit synthetic ReasonAllComplete rows for the audit trail
		// and continue without GetQualityProfile / ListEpisodeFiles.
		// Per-season decidePrefilter below stays as defense-in-depth
		// for the mixed-state path. R5: ~40 of 46 series in operator's
		// example were all-complete → ~40 wasted GetQualityProfile +
		// ListEpisodeFiles call pairs eliminated per scan.
		if seriesAllSeasonsComplete(s) {
			u.logger.DebugContext(ctx, "series_skipped_all_seasons_complete",
				slog.String("instance", inst.Config.Name),
				slog.Int("series_id", int(s.ID)),
				slog.String("series_title", s.Title),
				slog.Int("monitored_seasons", len(seasons)),
			)
			for _, season := range seasons {
				stats := series.SeasonStatsFromStatistics(season.Statistics)
				if _, err := u.evaluator.RecordSkip(ctx, evaluate.Input{
					ScanRunID: scanID,
					Instance:  instName,
					Series:    s,
					Season:    season,
					DryRun:    dryRun,
					Now:       time.Now().UTC(),
				}, decision.ReasonAllComplete, stats); err != nil {
					u.logger.WarnContext(ctx, "prefilter_record_skip_failed",
						slog.String("instance", inst.Config.Name),
						slog.Int("series_id", int(s.ID)),
						slog.Int("season_number", season.Number),
						slog.String("reason", string(decision.ReasonAllComplete)),
						slog.String("error", err.Error()))
				}
				observability.IncScanSkipped(instName, prefilterReasonLabel(decision.ReasonAllComplete))
				observability.SeriesEvaluated(instName, string(decision.OutcomeSkip))
			}
			continue
		}

		var profile ports.QualityProfile
		if cached, ok := profileCache[s.QualityProfile]; ok {
			profile = cached
		} else {
			p, perr := inst.Client.GetQualityProfile(ctx, s.QualityProfile)
			if perr != nil {
				if errors.Is(perr, domain.ErrInstanceUnauthorized) {
					return u.finalizeScanAborted(ctx, rec, inst, started, perr)
				}
				u.logger.WarnContext(ctx, "fetch quality profile failed",
					slog.Int("series_id", int(s.ID)),
					slog.Int("profile_id", s.QualityProfile),
					slog.String("error", perr.Error()),
				)
				errorsCount++
				pendingDelta = u.flushSeriesScannedIfDue(ctx, scanID, pendingDelta) // 011c: keep progress visible across transient errors
				continue
			}
			profileCache[s.QualityProfile] = p
			profile = p
		}

		fileQuality, ferr := inst.Client.ListEpisodeFiles(ctx, s.ID)
		if ferr != nil {
			if errors.Is(ferr, domain.ErrInstanceUnauthorized) {
				return u.finalizeScanAborted(ctx, rec, inst, started, ferr)
			}
			u.logger.WarnContext(ctx, "list episode files failed",
				slog.Int("series_id", int(s.ID)),
				slog.String("error", ferr.Error()),
			)
			errorsCount++
			pendingDelta = u.flushSeriesScannedIfDue(ctx, scanID, pendingDelta) // 011c: keep progress visible across transient errors
			continue
		}

		// Deferred-item #8: batch the series-cooldown lookup. Collect every
		// season key once, hit the repo once, then read from the local set
		// inside the inner loop. Shrinks N queries-per-series to 1.
		seriesCooldownActive := make(map[string]bool, len(seasons))
		if u.cooldowns != nil {
			keys := make([]string, 0, len(seasons))
			for _, season := range seasons {
				keys = append(keys, cooldown.SeriesKey(instName, s.ID, season.Number))
			}
			active, cdErr := u.cooldowns.FilterActive(ctx, cooldown.ScopeSeries, keys, time.Now().UTC())
			if cdErr != nil {
				u.logger.WarnContext(ctx, "cooldown lookup failed",
					slog.String("instance", inst.Config.Name),
					slog.Int("series_id", int(s.ID)),
					slog.Int("season_count", len(seasons)),
					slog.String("error", cdErr.Error()),
				)
			} else {
				for _, a := range active {
					seriesCooldownActive[a.Key] = true
				}
			}
		}

		// Live history is a fallback when the persistent origin_releases row is absent (M-new-1).
		// We only consult Sonarr history if no origin row exists for any season we'll evaluate.
		var liveOriginIndexer string
		var liveOriginFetched bool

		for _, season := range seasons {
			// 046b pre-filter: short-circuit complete + sonarr_handles
			// seasons BEFORE any Sonarr call (cooldown, ListEpisodes,
			// SearchReleases). Saves two Sonarr round-trips per skipped
			// season. Synthetic Decision row keeps the audit trail intact.
			prefilterStats := series.SeasonStatsFromStatistics(season.Statistics)
			if reason, skip := u.decidePrefilter(prefilterStats, inst); skip {
				if _, err := u.evaluator.RecordSkip(ctx, evaluate.Input{
					ScanRunID: scanID,
					Instance:  instName,
					Series:    s,
					Season:    season,
					DryRun:    dryRun,
					Now:       time.Now().UTC(),
				}, reason, prefilterStats); err != nil {
					u.logger.WarnContext(ctx, "prefilter_record_skip_failed",
						slog.String("instance", inst.Config.Name),
						slog.Int("series_id", int(s.ID)),
						slog.Int("season_number", season.Number),
						slog.String("reason", string(reason)),
						slog.String("error", err.Error()))
				}
				observability.IncScanSkipped(instName, prefilterReasonLabel(reason))
				observability.SeriesEvaluated(instName, string(decision.OutcomeSkip))
				continue
			}

			if u.cooldowns != nil {
				skey := cooldown.SeriesKey(instName, s.ID, season.Number)
				if seriesCooldownActive[skey] {
					u.logger.InfoContext(ctx, "season_evaluated",
						slog.String("instance", inst.Config.Name),
						slog.Int("series_id", int(s.ID)),
						slog.String("series_title", s.Title),
						slog.Int("season_number", season.Number),
						slog.String("decision", string(decision.OutcomeSkip)),
						slog.String("reason", string(decision.ReasonSkipSeriesCooldown)),
					)
					observability.SeriesEvaluated(instName, string(decision.OutcomeSkip))
					continue
				}
			}

			episodes, eerr := inst.Client.ListEpisodes(ctx, s.ID, season.Number)
			if eerr != nil {
				if errors.Is(eerr, domain.ErrInstanceUnauthorized) {
					return u.finalizeScanAborted(ctx, rec, inst, started, eerr)
				}
				u.logger.WarnContext(ctx, "list episodes failed",
					slog.Int("series_id", int(s.ID)),
					slog.Int("season", season.Number),
					slog.String("error", eerr.Error()),
				)
				errorsCount++
				continue
			}
			for i := range episodes {
				if q, ok := fileQuality[episodes[i].EpisodeFileID]; ok {
					episodes[i].QualityID = q
				}
			}
			season.Episodes = episodes

			originGUID := ""
			originIndexer := ""
			if u.origins != nil {
				if origin, found, oerr := u.origins.Get(ctx, instName, s.ID, season.Number); oerr == nil && found {
					originGUID = origin.GUID
					originIndexer = origin.IndexerName
				}
			}
			if originIndexer == "" {
				if !liveOriginFetched {
					if history, herr := inst.Client.GrabHistory(ctx, s.ID); herr == nil {
						liveOriginIndexer = dominantIndexer(history)
					} else {
						if errors.Is(herr, domain.ErrInstanceUnauthorized) {
							return u.finalizeScanAborted(ctx, rec, inst, started, herr)
						}
						u.logger.WarnContext(ctx, "fetch grab history failed",
							slog.Int("series_id", int(s.ID)),
							slog.String("error", herr.Error()),
						)
					}
					liveOriginFetched = true
				}
				originIndexer = liveOriginIndexer
			}

			d, evErr := u.evaluator.Execute(ctx, evaluate.Input{
				ScanRunID:            scanID,
				Instance:             instName,
				Sonarr:               inst.Client,
				Series:               s,
				Season:               season,
				Profile:              profile,
				OriginGUID:           originGUID,
				OriginIndexerName:    originIndexer,
				OriginBonus:          inst.Config.Ranking.OriginBonus,
				MinCustomFormatScore: inst.Config.Search.MinCustomFormatScore,
				RequireAllAired:      inst.Config.Search.RequireAllAired,
				SkipSpecials:         inst.Config.Search.SkipSpecials,
				SkipAnime:            inst.Config.Search.SkipAnime,
				DryRun:               dryRun,
				Now:                  time.Now().UTC(),
				Cooldowns:            u.cooldowns,
			})
			if evErr != nil {
				if errors.Is(evErr, domain.ErrInstanceUnauthorized) {
					return u.finalizeScanAborted(ctx, rec, inst, started, evErr)
				}
				errorsCount++
			}
			if d.CandidatesCount > 0 {
				candidates += d.CandidatesCount
			}

			// Real-grab path: only if grab UC wired, not dry-run, and evaluator selected a candidate.
			if !dryRun && u.grabUC != nil && d.Outcome == decision.OutcomeGrab && d.Selected != nil {
				if grabsDone >= maxGrabs {
					u.logger.InfoContext(ctx, "max_grabs_per_scan reached, deferring further grabs",
						slog.String("instance", inst.Config.Name),
						slog.Int("max_grabs_per_scan", maxGrabs),
					)
					continue
				}
				out := u.grabUC.Execute(ctx, grab.Input{
					ScanRunID:    scanID,
					InstanceName: instName,
					SeriesID:     s.ID,
					SeriesTitle:  s.Title,
					SeasonNumber: season.Number,
					Selected:     *d.Selected,
					Coverage:     d.Selected.Coverage,
					Sonarr:       inst.Client,
					Config: grab.Config{
						MaxAttempts:    inst.Config.Retry.MaxAttempts,
						InitialBackoff: inst.Config.Retry.InitialBackoff,
						MaxBackoff:     inst.Config.Retry.MaxBackoff,
						SeriesCooldown: inst.Config.Cooldown.SeriesAfterGrab,
						GUIDCooldown:   inst.Config.Cooldown.GUIDAfterFailedGrab,
					},
				})
				if out.Err == nil {
					grabsDone++
					consecutiveGrabFails = 0
				} else {
					grabsFailed++
					consecutiveGrabFails++
					errorsCount++
					if errors.Is(out.Err, domain.ErrInstanceUnauthorized) {
						rec.GrabsPerformed = grabsDone
						rec.GrabsFailed = grabsFailed
						rec.ErrorsCount = errorsCount
						rec.SeriesScanned = processed
						rec.CandidatesFound = candidates
						return u.finalizeScanAborted(ctx, rec, inst, started, out.Err)
					}
					if consecutiveGrabFails >= 3 {
						// D-2.4: transition the instance to UnavailableUnknown,
						// then finalize this scan as `failed`.
						if u.health != nil {
							u.logger.WarnContext(ctx, "instance_marked_unavailable",
								slog.String("instance", inst.Config.Name),
								slog.Int("series_id", int(s.ID)),
								slog.Int("consecutive_fails", consecutiveGrabFails),
								slog.String("transition_to", string(instance.HealthUnavailableUnknown)),
								slog.String("reason", "3 consecutive grab_failed"),
							)
							u.health.MarkUnavailable(inst.Config.Name, instance.HealthUnavailableUnknown,
								"3 consecutive grab_failed", time.Now().UTC())
						}
						rec.GrabsPerformed = grabsDone
						rec.GrabsFailed = grabsFailed
						rec.ErrorsCount = errorsCount
						rec.SeriesScanned = processed
						rec.CandidatesFound = candidates
						return u.finalizeScanFailed(ctx, rec, inst, started, fmt.Errorf("aborting after 3 consecutive grab_failed: %w", out.Err))
					}
				}
			}
		}
		// 011c: flush incremental progress every N=5 series.
		pendingDelta = u.flushSeriesScannedIfDue(ctx, scanID, pendingDelta)
	}

	// 012: user-cancel path. Carry partial counters into the cancelled
	// finalize. Decisions/grabs already written stay.
	if errors.Is(ctx.Err(), context.Canceled) {
		rec.SeriesScanned = processed
		rec.CandidatesFound = candidates
		rec.GrabsPerformed = grabsDone
		rec.GrabsFailed = grabsFailed
		rec.ErrorsCount = errorsCount
		return u.finalizeScanCancelled(ctx, rec, inst, started)
	}

	finished := time.Now().UTC()
	rec.Status = "completed"
	rec.SeriesScanned = processed
	rec.CandidatesFound = candidates
	rec.GrabsPerformed = grabsDone
	rec.GrabsFailed = grabsFailed
	rec.ErrorsCount = errorsCount
	rec.FinishedAt = &finished
	// Detached writeCtx: a Cancel arriving between the loop-exit ctx.Err()
	// check and this Update would otherwise make GORM abort with
	// context.Canceled, leaving the row stuck at status="running".
	writeCtx := logger.WithTraceID(context.Background(), rec.ID.String())
	if err := u.scans.Update(writeCtx, rec); err != nil {
		u.logger.ErrorContext(ctx, "update scan record failed", slog.String("error", err.Error()))
	}
	observability.ObserveScanDuration(shareddomain.InstanceName(inst.Config.Name), finished.Sub(started).Seconds())
	observability.ScanCompleted(shareddomain.InstanceName(inst.Config.Name), "completed")

	u.logger.InfoContext(ctx, "scan_completed",
		slog.String("instance", inst.Config.Name),
		slog.Int("series_scanned", processed),
		slog.Int("candidates_found", candidates),
		slog.Int("grabs_performed", grabsDone),
		slog.Int("grabs_failed", grabsFailed),
		slog.Int("errors", errorsCount),
		slog.Float64("duration_seconds", finished.Sub(started).Seconds()),
	)

	return RunResult{
		ScanRunID:    scanID,
		InstanceName: instName,
		Status:       "completed",
		Started:      started,
		Finished:     finished,
		Series:       processed,
		Candidates:   candidates,
		Grabs:        grabsDone,
		GrabsFailed:  grabsFailed,
		Errors:       errorsCount,
	}, nil
}

// finalizeScanAborted marks the scan as `aborted` (mid-scan auth abort per
// D-2.3) and transitions the instance to UnavailableAuth.
func (u *UseCase) finalizeScanAborted(ctx context.Context, rec ports.ScanRecord, inst Instance, started time.Time, cause error) (RunResult, error) {
	instName := shareddomain.InstanceName(inst.Config.Name)
	rec.Status = "aborted"
	if cause != nil {
		// F-P2-4: cap at 4 KiB (errtext.MaxBytes). cause is typically a
		// wrapped sonarr.StatusError carrying the full upstream body.
		rec.ErrorMessage = errtext.Clamp(cause.Error())
	}
	finish := time.Now().UTC()
	rec.FinishedAt = &finish
	// Detached writeCtx: ctx may already be Cancelled — see processScan's
	// terminal Update for full rationale.
	writeCtx := logger.WithTraceID(context.Background(), rec.ID.String())
	if err := u.scans.Update(writeCtx, rec); err != nil {
		u.logger.ErrorContext(ctx, "finalize_update_failed",
			slog.String("status", rec.Status),
			slog.String("error", err.Error()))
	}
	observability.ObserveScanDuration(shareddomain.InstanceName(inst.Config.Name), finish.Sub(started).Seconds())
	observability.ScanCompleted(shareddomain.InstanceName(inst.Config.Name), "aborted")
	if u.health != nil {
		u.health.MarkUnavailable(inst.Config.Name, instance.HealthUnavailableAuth, cause.Error(), finish)
	}
	u.logger.ErrorContext(ctx, "scan_aborted_unauthorized",
		slog.String("instance", inst.Config.Name),
		slog.String("action_required", "verify SEASONFILL_API_KEY or sonarr_instances.api_key"),
		slog.String("error", cause.Error()),
	)
	return RunResult{
		ScanRunID:    rec.ID,
		InstanceName: instName,
		Status:       "aborted",
		Started:      started,
		Finished:     finish,
		Series:       rec.SeriesScanned,
		Candidates:   rec.CandidatesFound,
		Grabs:        rec.GrabsPerformed,
		GrabsFailed:  rec.GrabsFailed,
		Errors:       rec.ErrorsCount,
	}, cause
}

func (u *UseCase) finalizeScanFailed(ctx context.Context, rec ports.ScanRecord, inst Instance, started time.Time, cause error) (RunResult, error) {
	instName := shareddomain.InstanceName(inst.Config.Name)
	rec.Status = "failed"
	if cause != nil {
		// F-P2-4: cap at 4 KiB (errtext.MaxBytes). Matches finalizeScanAborted.
		rec.ErrorMessage = errtext.Clamp(cause.Error())
	}
	finish := time.Now().UTC()
	rec.FinishedAt = &finish
	// Detached writeCtx: ctx may already be Cancelled — see processScan's
	// terminal Update for full rationale.
	writeCtx := logger.WithTraceID(context.Background(), rec.ID.String())
	if err := u.scans.Update(writeCtx, rec); err != nil {
		u.logger.ErrorContext(ctx, "finalize_update_failed",
			slog.String("status", rec.Status),
			slog.String("error", err.Error()))
	}
	observability.ObserveScanDuration(shareddomain.InstanceName(inst.Config.Name), finish.Sub(started).Seconds())
	observability.ScanCompleted(shareddomain.InstanceName(inst.Config.Name), "failed")
	return RunResult{
		ScanRunID:    rec.ID,
		InstanceName: instName,
		Status:       "failed",
		Started:      started,
		Finished:     finish,
		Series:       rec.SeriesScanned,
		Candidates:   rec.CandidatesFound,
		Grabs:        rec.GrabsPerformed,
		GrabsFailed:  rec.GrabsFailed,
		Errors:       rec.ErrorsCount,
	}, cause
}

func (u *UseCase) finalizeScanCancelled(ctx context.Context, rec ports.ScanRecord, inst Instance, started time.Time) (RunResult, error) {
	instName := shareddomain.InstanceName(inst.Config.Name)
	rec.Status = "cancelled"
	rec.ErrorMessage = "user requested cancellation"
	finish := time.Now().UTC()
	rec.FinishedAt = &finish
	writeCtx := logger.WithTraceID(context.Background(), rec.ID.String())
	if err := u.scans.Update(writeCtx, rec); err != nil {
		u.logger.ErrorContext(ctx, "update scan record failed (cancelled)",
			slog.String("error", err.Error()))
		return RunResult{
			ScanRunID: rec.ID, InstanceName: instName, Status: "cancelled",
			Started: started, Finished: finish, Series: rec.SeriesScanned,
			Candidates: rec.CandidatesFound, Grabs: rec.GrabsPerformed,
			GrabsFailed: rec.GrabsFailed, Errors: rec.ErrorsCount,
		}, err
	}
	observability.ObserveScanDuration(shareddomain.InstanceName(inst.Config.Name), finish.Sub(started).Seconds())
	observability.ScanCompleted(shareddomain.InstanceName(inst.Config.Name), "cancelled")
	u.logger.InfoContext(ctx, "scan_cancelled",
		slog.String("instance", inst.Config.Name),
		slog.Int("series_scanned", rec.SeriesScanned),
		slog.Int("grabs_performed", rec.GrabsPerformed),
		slog.Float64("duration_seconds", finish.Sub(started).Seconds()),
	)
	return RunResult{
		ScanRunID: rec.ID, InstanceName: instName, Status: "cancelled",
		Started: started, Finished: finish, Series: rec.SeriesScanned,
		Candidates: rec.CandidatesFound, Grabs: rec.GrabsPerformed,
		GrabsFailed: rec.GrabsFailed, Errors: rec.ErrorsCount,
	}, nil
}

// acquire grabs the per-instance inflight slot with a no-op CancelFunc.
// Sync path (runOne) leaves it. Async path swaps in the real
// CancelFunc via setInflightCancel right after acquire succeeds.
func (u *UseCase) acquire(instance shareddomain.InstanceName, scanID uuid.UUID) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.inflight[instance]; ok {
		return fmt.Errorf("%w: %s", ErrScanAlreadyRunning, instance)
	}
	u.inflight[instance] = inflightEntry{ID: scanID, Cancel: func() {}}
	return nil
}

func (u *UseCase) setInflightCancel(instance shareddomain.InstanceName, cancel context.CancelFunc) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if e, ok := u.inflight[instance]; ok {
		e.Cancel = cancel
		u.inflight[instance] = e
	}
}

func (u *UseCase) release(instance shareddomain.InstanceName) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.inflight, instance)
}

// Cancel signals cancellation of the running scan identified by scanID.
// Returns ErrScanNotRunning when no inflight entry matches (unknown id
// or scan already terminal). Fires the CancelFunc and returns — does
// NOT block waiting for the goroutine to observe the signal.
func (u *UseCase) Cancel(_ context.Context, scanID uuid.UUID) error {
	u.mu.Lock()
	var cancel context.CancelFunc
	for _, e := range u.inflight {
		if e.ID == scanID {
			cancel = e.Cancel
			break
		}
	}
	u.mu.Unlock()
	if cancel == nil {
		return fmt.Errorf("%w: %s", ErrScanNotRunning, scanID)
	}
	cancel()
	return nil
}

type tagFilter struct {
	mode    string
	include map[int]struct{}
	exclude map[int]struct{}
}

func (t tagFilter) skip(seriesTags []int) (string, bool) {
	if len(t.exclude) > 0 {
		for _, id := range seriesTags {
			if _, ok := t.exclude[id]; ok {
				return "tag_excluded", true
			}
		}
	}
	if len(t.include) == 0 {
		return "", false
	}
	seriesSet := make(map[int]struct{}, len(seriesTags))
	for _, id := range seriesTags {
		seriesSet[id] = struct{}{}
	}
	switch t.mode {
	case "all":
		for id := range t.include {
			if _, ok := seriesSet[id]; !ok {
				return "tag_include_missing", true
			}
		}
		return "", false
	default: // "any" or unset
		for id := range t.include {
			if _, ok := seriesSet[id]; ok {
				return "", false
			}
		}
		return "tag_no_include_match", true
	}
}

// dominantIndexer counts every grabbed history record by IndexerName. Ties are
// broken deterministically by indexer name ascending (N-new-2).
func dominantIndexer(history []ports.HistoryEvent) string {
	if len(history) == 0 {
		return ""
	}
	counts := make(map[string]int, 4)
	for _, h := range history {
		if h.IndexerName == "" {
			continue
		}
		counts[h.IndexerName]++
	}
	if len(counts) == 0 {
		return ""
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	best := ""
	bestCount := -1
	for _, name := range names {
		if counts[name] > bestCount {
			best = name
			bestCount = counts[name]
		}
	}
	return best
}

// prefilterReasonLabel is the metric-label form of a pre-filter Reason.
// Grafana dashboards key on `reason="all_complete"` not
// `reason="skip_all_complete"` — see PRD §3 B4 wording.
func prefilterReasonLabel(r decision.Reason) string {
	switch r {
	case decision.ReasonAllComplete:
		return "all_complete"
	case decision.ReasonSonarrHandles:
		return "sonarr_handles"
	default:
		return string(r)
	}
}

// decidePrefilter inspects per-season Statistics and returns a skip
// Reason when the season can be short-circuited without calling Sonarr.
// Rules — PRD §3 B4 acceptance #3-4:
//
//   - stats.IsComplete()                    → (ReasonAllComplete, true)   UNCONDITIONAL
//   - flag && stats.HasNoLocal()            → (ReasonSonarrHandles, true) flag-gated
//   - otherwise                             → ("", false)
//
// `all_complete` is NOT flag-gated: a season with no missing episodes
// has nothing for seasonfill, and the legacy evaluator would skip it
// one Sonarr call later anyway. Honouring `false` here would only burn
// quota without changing the outcome.
func (u *UseCase) decidePrefilter(stats series.SeasonStats, inst Instance) (decision.Reason, bool) {
	if stats.IsComplete() {
		return decision.ReasonAllComplete, true
	}
	if inst.Config.ScanSkipHandledSeasons && stats.HasNoLocal() {
		return decision.ReasonSonarrHandles, true
	}
	return "", false
}

// seriesAllSeasonsComplete reports whether EVERY monitored season is
// SeasonStats.IsComplete(). Callers use it as a series-level fast-path
// to skip GetQualityProfile / ListEpisodeFiles when no season has work.
// Series with zero monitored seasons return false so the existing
// "len(seasons) == 0" guard upstream stays the sole owner of that path
// (avoids surprising every-empty-set-is-true semantics).
func seriesAllSeasonsComplete(s series.Series) bool {
	monitored := s.MonitoredSeasons()
	if len(monitored) == 0 {
		return false
	}
	for _, season := range monitored {
		if !series.SeasonStatsFromStatistics(season.Statistics).IsComplete() {
			return false
		}
	}
	return true
}

// flushSeriesScannedIfDue commits pending series_scanned delta when it
// reaches N=5. Returns the new (post-flush or unchanged) delta. Errors
// are WARN-logged — they don't fail the scan (the final Update rewrites
// the full row anyway). Threshold is a code-internal write-batching
// knob, not user-tunable surface.
const scanProgressFlushEvery = 5

func (u *UseCase) flushSeriesScannedIfDue(ctx context.Context, id uuid.UUID, pending int) int {
	if pending < scanProgressFlushEvery {
		return pending
	}
	if err := u.scans.IncrementSeriesScanned(ctx, id, pending); err != nil {
		u.logger.WarnContext(ctx, "increment_series_scanned_failed",
			slog.String("scan_id", id.String()),
			slog.Int("delta", pending),
			slog.String("error", err.Error()))
	}
	return 0
}

// fillSeriesCache lazily refreshes series_cache and season_stats for the
// instance. Runs after every successful ListSeries. Errors are warn-logged
// and NEVER propagate — this is a best-effort sidecar (D-2.5 pattern).
// No-ops for series_cache when u.seriesCache is unset; no-ops for
// season_stats when u.seasonStats is unset.
//
// Story 380: seriesList is the already-fetched ListSeries result the caller
// holds. We use its Seasons[].Statistics block to write season_stats per
// season — the writer was only wired into the webhook E-1 path before so
// scans that ran without any webhook activity left season_stats empty,
// which made SeriesSeasonsAccordion render 0/N for every season the
// scan_skip_handled_seasons fast-path covered.
func (u *UseCase) fillSeriesCache(ctx context.Context, inst Instance, seriesList []series.Series) {
	instName := shareddomain.InstanceName(inst.Config.Name)
	if u.seriesCache != nil {
		entries, err := inst.Client.ListSeriesCache(ctx, instName)
		if err != nil {
			u.logger.WarnContext(ctx, "series_cache_list_failed",
				slog.String("instance", inst.Config.Name),
				slog.String("error", err.Error()),
			)
		} else {
			for _, e := range entries {
				if uerr := u.seriesCache.Upsert(ctx, e); uerr != nil {
					u.logger.WarnContext(ctx, "series_cache_upsert_failed",
						slog.String("instance", inst.Config.Name),
						slog.Int("series_id", int(e.SonarrSeriesID)),
						slog.String("error", uerr.Error()),
					)
				}
			}
		}
	}

	if u.seasonStats == nil {
		return
	}
	for _, s := range seriesList {
		for _, season := range s.Seasons {
			// Same aired fallback as cacheEntryFromPayload: Sonarr's
			// per-season block reliably ships airedEpisodeCount, but
			// belt-and-braces in case a future Sonarr response omits
			// it the way the series-level block does.
			aired := season.Statistics.Aired
			if aired == 0 {
				aired = season.Statistics.EpisodeCount
			}
			stat := series.SeasonStat{
				InstanceName:      instName,
				SonarrSeriesID:    s.ID,
				SeasonNumber:      season.Number,
				Monitored:         season.Monitored,
				EpisodeCount:      season.Statistics.EpisodeCount,
				EpisodeFileCount:  season.Statistics.EpisodeFileCount,
				TotalEpisodeCount: season.Statistics.Total,
				AiredEpisodeCount: aired,
				SizeOnDiskBytes:   season.Statistics.SizeOnDisk,
			}
			if uerr := u.seasonStats.Upsert(ctx, stat); uerr != nil {
				u.logger.WarnContext(ctx, "season_stats_upsert_failed",
					slog.String("instance", inst.Config.Name),
					slog.Int("series_id", int(s.ID)),
					slog.Int("season_number", season.Number),
					slog.String("error", uerr.Error()),
				)
			}
		}
	}
}

// buildTagFilter resolves the configured `tags.include` / `tags.exclude`
// label lists to Sonarr tag IDs.
//
// M-6 fail-CLOSED rule: if `Include` is non-empty AND none of the configured
// labels resolved to a Sonarr tag ID (typos, Sonarr-side renames, empty tag
// list), return an error rather than a permissive empty include filter. The
// scan-loop call-site already aborts the scan with `Status=failed` when
// `Include` is non-empty AND `buildTagFilter` errors. Without this rule the
// include filter silently degraded to "no include filter" -> scanned every
// series, identical blast-radius risk to D-1.4 / D-2.5.
//
// Exclude-only and unconfigured filters stay fail-open: a flaky tag endpoint
// or missing exclude label cannot expand scope in those modes.
func buildTagFilter(ctx context.Context, inst Instance) (tagFilter, error) {
	include := inst.Config.Tags.Include
	exclude := inst.Config.Tags.Exclude
	if len(include) == 0 && len(exclude) == 0 {
		return tagFilter{mode: inst.Config.Tags.Mode}, nil
	}
	tags, err := inst.Client.ListTags(ctx)
	if err != nil {
		return tagFilter{mode: inst.Config.Tags.Mode}, fmt.Errorf("list tags: %w", err)
	}
	byLabel := make(map[string]int, len(tags))
	for _, t := range tags {
		byLabel[t.Label] = t.ID
	}
	f := tagFilter{mode: inst.Config.Tags.Mode}
	if len(include) > 0 {
		f.include = make(map[int]struct{}, len(include))
		for _, label := range include {
			if id, ok := byLabel[label]; ok {
				f.include[id] = struct{}{}
			}
		}
		if len(f.include) == 0 {
			return tagFilter{mode: inst.Config.Tags.Mode},
				fmt.Errorf("tag include filter is non-empty but no labels matched any sonarr tag: %v", include)
		}
	}
	if len(exclude) > 0 {
		f.exclude = make(map[int]struct{}, len(exclude))
		for _, label := range exclude {
			if id, ok := byLabel[label]; ok {
				f.exclude[id] = struct{}{}
			}
		}
	}
	return f, nil
}
