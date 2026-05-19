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

var ErrScanAlreadyRunning = errors.New("scan already running for instance")

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
	return RunResult{}, fmt.Errorf("unknown instance: %s", name)
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

	profileCache := make(map[int]ports.QualityProfile)

	processed := 0
	candidates := 0
	errorsCount := 0
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
			fileQuality, ferr := inst.Client.ListEpisodeFiles(ctx, s.ID)
			if ferr != nil {
				u.logger.WarnContext(ctx, "list episode files failed",
					slog.Int("series_id", s.ID),
					slog.String("error", ferr.Error()),
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
