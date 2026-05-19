package scan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
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

type Instance struct {
	Config config.SonarrInstance
	Client ports.SonarrClient
}

type UseCase struct {
	instances []Instance
	evaluator *evaluate.UseCase
	scans     ports.ScanRepository
	logger    *slog.Logger
	dryRun    bool

	mu       sync.Mutex
	inflight map[string]uuid.UUID
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

type RunResult struct {
	ScanRunID    uuid.UUID
	InstanceName string
	Status       string
	Started      time.Time
	Finished     time.Time
	Series       int
	Candidates   int
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

func (u *UseCase) runOne(parent context.Context, inst Instance, trigger Trigger) (RunResult, error) {
	scanID := uuid.New()
	ctx := logger.WithTraceID(parent, scanID.String())

	if err := u.acquire(inst.Config.Name, scanID); err != nil {
		return RunResult{InstanceName: inst.Config.Name, Status: "conflict"}, err
	}
	defer u.release(inst.Config.Name)

	observability.IncActiveScans(inst.Config.Name)
	defer observability.DecActiveScans(inst.Config.Name)

	started := time.Now().UTC()
	rec := ports.ScanRecord{
		ID:           scanID,
		InstanceName: inst.Config.Name,
		Trigger:      string(trigger),
		StartedAt:    started,
		Status:       "running",
		DryRun:       u.dryRun,
	}
	if err := u.scans.Create(ctx, rec); err != nil {
		u.logger.ErrorContext(ctx, "create scan record failed", slog.String("error", err.Error()))
		return RunResult{ScanRunID: scanID, InstanceName: inst.Config.Name, Status: "failed"}, err
	}

	u.logger.InfoContext(ctx, "scan_started",
		slog.String("instance", inst.Config.Name),
		slog.String("trigger", string(trigger)),
		slog.Bool("dry_run", u.dryRun),
	)

	seriesList, err := inst.Client.ListSeries(ctx)
	if err != nil {
		rec.Status = "failed"
		rec.ErrorMessage = err.Error()
		finish := time.Now().UTC()
		rec.FinishedAt = &finish
		_ = u.scans.Update(ctx, rec)
		observability.ScanCompleted(inst.Config.Name, "failed")
		return RunResult{ScanRunID: scanID, InstanceName: inst.Config.Name, Status: "failed"}, fmt.Errorf("list series: %w", err)
	}

	processed := 0
	candidates := 0
	errorsCount := 0

	tagFilter, err := buildTagFilter(ctx, inst)
	if err != nil {
		u.logger.WarnContext(ctx, "resolve tags failed", slog.String("error", err.Error()))
		errorsCount++
	}

	profileCache := make(map[int]ports.QualityProfile)

	maxSeries := inst.Config.Limits.ScanMaxSeries
	if maxSeries <= 0 {
		maxSeries = len(seriesList)
	}

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

		originIndexer := ""
		if history, herr := inst.Client.GrabHistory(ctx, s.ID); herr == nil {
			originIndexer = dominantIndexer(history)
		} else {
			u.logger.WarnContext(ctx, "fetch grab history failed",
				slog.Int("series_id", s.ID),
				slog.String("error", herr.Error()),
			)
		}

		for _, season := range seasons {
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

			d, evErr := u.evaluator.Execute(ctx, evaluate.Input{
				ScanRunID:            scanID,
				Instance:             inst.Config.Name,
				Sonarr:               inst.Client,
				Series:               s,
				Season:               season,
				Profile:              profile,
				OriginIndexerName:    originIndexer,
				OriginBonus:          inst.Config.Ranking.OriginBonus,
				MinCustomFormatScore: inst.Config.Search.MinCustomFormatScore,
				RequireAllAired:      inst.Config.Search.RequireAllAired,
				SkipSpecials:         inst.Config.Search.SkipSpecials,
				SkipAnime:            inst.Config.Search.SkipAnime,
				DryRun:               u.dryRun,
				Now:                  time.Now().UTC(),
			})
			if evErr != nil {
				errorsCount++
			}
			if d.CandidatesCount > 0 {
				candidates += d.CandidatesCount
			}
		}
	}

	finished := time.Now().UTC()
	rec.Status = "completed"
	rec.SeriesScanned = processed
	rec.CandidatesFound = candidates
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
		Errors:       errorsCount,
	}, nil
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
	best := ""
	bestCount := 0
	for name, count := range counts {
		if count > bestCount {
			best = name
			bestCount = count
		}
	}
	return best
}

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
