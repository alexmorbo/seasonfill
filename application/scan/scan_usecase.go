package scan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

var (
	ErrScanAlreadyRunning = errors.New("scan already running for instance")
	ErrUnknownInstance    = errors.New("unknown instance")
)

type Trigger string

const (
	TriggerCron    Trigger = "cron"
	TriggerManual  Trigger = "manual"
	TriggerStartup Trigger = "startup"
)

// Barrier is an optional synchronization hook used by tests to make
// runOne deterministically overlap two goroutines. nil in production.
type Barrier interface {
	Reached(instance string)
}

type Instance struct {
	Config config.SonarrInstance
	Client ports.SonarrClient
}

type UseCase struct {
	instances []Instance
	evaluator *evaluate.UseCase
	scans     ports.ScanRepository
	grabUC    *grab.UseCase
	cooldowns ports.CooldownRepository
	origins   ports.OriginReleaseRepository
	logger    *slog.Logger
	dryRun    bool

	mu       sync.Mutex
	inflight map[string]uuid.UUID

	barrier Barrier
}

func NewUseCase(
	instances []Instance,
	evaluator *evaluate.UseCase,
	scans ports.ScanRepository,
	logger *slog.Logger,
	dryRun bool,
) *UseCase {
	return &UseCase{
		instances: instances,
		evaluator: evaluator,
		scans:     scans,
		logger:    logger,
		dryRun:    dryRun,
		inflight:  make(map[string]uuid.UUID),
	}
}

// WithGrabUseCase wires the (optional) real-grab path. Without it, scans
// behave as in Phase 1 (no POST).
func (u *UseCase) WithGrabUseCase(g *grab.UseCase) *UseCase { u.grabUC = g; return u }

// WithCooldowns wires the (optional) cooldown repository.
func (u *UseCase) WithCooldowns(c ports.CooldownRepository) *UseCase { u.cooldowns = c; return u }

// WithOrigins wires the (optional) origin_releases repository.
func (u *UseCase) WithOrigins(o ports.OriginReleaseRepository) *UseCase { u.origins = o; return u }

// WithBarrier injects a test-only synchronization hook.
func (u *UseCase) WithBarrier(b Barrier) *UseCase { u.barrier = b; return u }

type RunResult struct {
	ScanRunID    uuid.UUID
	InstanceName string
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
		out[k] = v
	}
	return out
}

func (u *UseCase) Run(parent context.Context, trigger Trigger) ([]RunResult, error) {
	results := make([]RunResult, len(u.instances))
	errs := make([]error, len(u.instances))

	var wg sync.WaitGroup
	for i := range u.instances {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := u.runOne(parent, u.instances[idx], trigger)
			results[idx] = res
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	return results, errors.Join(errs...)
}

func (u *UseCase) RunInstance(parent context.Context, name string, trigger Trigger) (RunResult, error) {
	for _, inst := range u.instances {
		if inst.Config.Name == name {
			return u.runOne(parent, inst, trigger)
		}
	}
	return RunResult{}, fmt.Errorf("%w: %s", ErrUnknownInstance, name)
}

// instanceDryRun decides the effective dry-run flag for one instance.
// Instance override wins per D-2.6. Reads the per-instance *bool directly —
// nil means "no override, use global".
func (u *UseCase) instanceDryRun(inst Instance) bool {
	if inst.Config.DryRun != nil {
		return *inst.Config.DryRun
	}
	return u.dryRun
}

func (u *UseCase) runOne(parent context.Context, inst Instance, trigger Trigger) (RunResult, error) {
	scanID := uuid.New()
	ctx := logger.WithTraceID(parent, scanID.String())

	if err := u.acquire(inst.Config.Name, scanID); err != nil {
		return RunResult{InstanceName: inst.Config.Name, Status: "conflict"}, err
	}
	defer u.release(inst.Config.Name)

	if u.barrier != nil {
		u.barrier.Reached(inst.Config.Name)
	}

	observability.IncActiveScans(inst.Config.Name)
	defer observability.DecActiveScans(inst.Config.Name)

	dryRun := u.instanceDryRun(inst)
	started := time.Now().UTC()
	rec := ports.ScanRecord{
		ID:           scanID,
		InstanceName: inst.Config.Name,
		Trigger:      string(trigger),
		StartedAt:    started,
		Status:       "running",
		DryRun:       dryRun,
	}
	if err := u.scans.Create(ctx, rec); err != nil {
		u.logger.ErrorContext(ctx, "create scan record failed", slog.String("error", err.Error()))
		return RunResult{ScanRunID: scanID, InstanceName: inst.Config.Name, Status: "failed"}, err
	}

	u.logger.InfoContext(ctx, "scan_started",
		slog.String("instance", inst.Config.Name),
		slog.String("trigger", string(trigger)),
		slog.Bool("dry_run", dryRun),
	)

	seriesList, err := inst.Client.ListSeries(ctx)
	if err != nil {
		return u.finalizeScanFailed(ctx, rec, inst, started, fmt.Errorf("list series: %w", err))
	}

	tagFilter, tagErr := buildTagFilter(ctx, inst)
	if tagErr != nil {
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
				slog.Int("series_id", s.ID),
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

		var profile ports.QualityProfile
		if cached, ok := profileCache[s.QualityProfile]; ok {
			profile = cached
		} else {
			p, perr := inst.Client.GetQualityProfile(ctx, s.QualityProfile)
			if perr != nil {
				u.logger.WarnContext(ctx, "fetch quality profile failed",
					slog.Int("series_id", s.ID),
					slog.Int("profile_id", s.QualityProfile),
					slog.String("error", perr.Error()),
				)
				errorsCount++
				continue
			}
			profileCache[s.QualityProfile] = p
			profile = p
		}

		fileQuality, ferr := inst.Client.ListEpisodeFiles(ctx, s.ID)
		if ferr != nil {
			u.logger.WarnContext(ctx, "list episode files failed",
				slog.Int("series_id", s.ID),
				slog.String("error", ferr.Error()),
			)
			errorsCount++
			continue
		}

		// Live history is a fallback when the persistent origin_releases row is absent (M-new-1).
		// We only consult Sonarr history if no origin row exists for any season we'll evaluate.
		var liveOriginIndexer string
		var liveOriginFetched bool

		for _, season := range seasons {
			// Series cooldown filter at scan-loop level.
			if u.cooldowns != nil {
				skey := cooldown.SeriesKey(inst.Config.Name, s.ID, season.Number)
				active, cdErr := u.cooldowns.FilterActive(ctx, cooldown.ScopeSeries, []string{skey}, time.Now().UTC())
				if cdErr != nil {
					u.logger.WarnContext(ctx, "cooldown lookup failed",
						slog.String("instance", inst.Config.Name),
						slog.Int("series_id", s.ID),
						slog.Int("season", season.Number),
						slog.String("error", cdErr.Error()),
					)
				} else if len(active) > 0 {
					u.logger.InfoContext(ctx, "season_evaluated",
						slog.String("instance", inst.Config.Name),
						slog.Int("series_id", s.ID),
						slog.String("series_title", s.Title),
						slog.Int("season_number", season.Number),
						slog.String("decision", string(decision.OutcomeSkip)),
						slog.String("reason", string(decision.ReasonSkipSeriesCooldown)),
					)
					observability.SeriesEvaluated(inst.Config.Name, string(decision.OutcomeSkip))
					continue
				}
			}

			episodes, eerr := inst.Client.ListEpisodes(ctx, s.ID, season.Number)
			if eerr != nil {
				u.logger.WarnContext(ctx, "list episodes failed",
					slog.Int("series_id", s.ID),
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
				if origin, found, oerr := u.origins.Get(ctx, inst.Config.Name, s.ID, season.Number); oerr == nil && found {
					originGUID = origin.GUID
					originIndexer = origin.IndexerName
				}
			}
			if originIndexer == "" {
				if !liveOriginFetched {
					if history, herr := inst.Client.GrabHistory(ctx, s.ID); herr == nil {
						liveOriginIndexer = dominantIndexer(history)
					} else {
						u.logger.WarnContext(ctx, "fetch grab history failed",
							slog.Int("series_id", s.ID),
							slog.String("error", herr.Error()),
						)
					}
					liveOriginFetched = true
				}
				originIndexer = liveOriginIndexer
			}

			d, evErr := u.evaluator.Execute(ctx, evaluate.Input{
				ScanRunID:            scanID,
				Instance:             inst.Config.Name,
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
					InstanceName: inst.Config.Name,
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
					if consecutiveGrabFails >= 3 {
						// D-2.4: abort scan after 3 consecutive grab_failed.
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
	}

	finished := time.Now().UTC()
	rec.Status = "completed"
	rec.SeriesScanned = processed
	rec.CandidatesFound = candidates
	rec.GrabsPerformed = grabsDone
	rec.GrabsFailed = grabsFailed
	rec.ErrorsCount = errorsCount
	rec.FinishedAt = &finished
	if err := u.scans.Update(ctx, rec); err != nil {
		u.logger.ErrorContext(ctx, "update scan record failed", slog.String("error", err.Error()))
	}
	observability.ObserveScanDuration(inst.Config.Name, finished.Sub(started).Seconds())
	observability.ScanCompleted(inst.Config.Name, "completed")

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
		InstanceName: inst.Config.Name,
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

func (u *UseCase) finalizeScanFailed(ctx context.Context, rec ports.ScanRecord, inst Instance, started time.Time, cause error) (RunResult, error) {
	rec.Status = "failed"
	if cause != nil {
		rec.ErrorMessage = cause.Error()
	}
	finish := time.Now().UTC()
	rec.FinishedAt = &finish
	_ = u.scans.Update(ctx, rec)
	observability.ObserveScanDuration(inst.Config.Name, finish.Sub(started).Seconds())
	observability.ScanCompleted(inst.Config.Name, "failed")
	return RunResult{
		ScanRunID:    rec.ID,
		InstanceName: inst.Config.Name,
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

func (u *UseCase) acquire(instance string, scanID uuid.UUID) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.inflight[instance]; ok {
		return fmt.Errorf("%w: %s", ErrScanAlreadyRunning, instance)
	}
	u.inflight[instance] = scanID
	return nil
}

func (u *UseCase) release(instance string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.inflight, instance)
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
