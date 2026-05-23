// Package rescan implements story 017.
package rescan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/series"
)

var (
	ErrAlreadySuperseded = errors.New("decision already superseded")
	ErrAlreadyExecuted   = errors.New("decision already executed against sonarr")
)

type UseCase struct {
	decisions ports.DecisionRepository
	grabs     ports.GrabRepository
	evaluator *evaluate.UseCase
	instances map[string]scan.Instance
	logger    *slog.Logger
}

func NewUseCase(decisions ports.DecisionRepository, grabs ports.GrabRepository,
	evaluator *evaluate.UseCase, instances map[string]scan.Instance,
	logger *slog.Logger) *UseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &UseCase{decisions: decisions, grabs: grabs, evaluator: evaluator,
		instances: instances, logger: logger}
}

type Input struct {
	DecisionID uuid.UUID
}

type Output struct {
	NewDecision decision.Decision
}

func (u *UseCase) Execute(ctx context.Context, in Input) (Output, error) {
	original, err := u.decisions.GetByID(ctx, in.DecisionID)
	if err != nil {
		return Output{}, fmt.Errorf("load decision: %w", err)
	}
	if original.SupersededByID != nil {
		return Output{}, fmt.Errorf("%w: %s", ErrAlreadySuperseded, original.ID)
	}
	if err := u.checkNotExecuted(ctx, original); err != nil {
		return Output{}, err
	}

	inst, ok := u.instances[original.InstanceName]
	if !ok {
		return Output{}, fmt.Errorf("unknown instance: %s", original.InstanceName)
	}

	seriesRow, ssErr := inst.Client.GetSeries(ctx, original.SeriesID)
	if ssErr != nil {
		return Output{}, fmt.Errorf("get series: %w", ssErr)
	}
	episodes, epErr := inst.Client.ListEpisodes(ctx, original.SeriesID, original.SeasonNumber)
	if epErr != nil {
		return Output{}, fmt.Errorf("list episodes: %w", epErr)
	}
	fileQuality, fqErr := inst.Client.ListEpisodeFiles(ctx, original.SeriesID)
	if fqErr != nil {
		return Output{}, fmt.Errorf("list episode files: %w", fqErr)
	}
	profile, prErr := inst.Client.GetQualityProfile(ctx, seriesRow.QualityProfile)
	if prErr != nil {
		return Output{}, fmt.Errorf("get quality profile: %w", prErr)
	}
	for i := range episodes {
		if q, ok := fileQuality[episodes[i].EpisodeFileID]; ok {
			episodes[i].QualityID = q
		}
	}
	season := series.Season{
		Number: original.SeasonNumber, Monitored: true, // operator-explicit
		Episodes: episodes,
	}
	dryRun := false
	if inst.Config.DryRun != nil {
		dryRun = *inst.Config.DryRun
	}

	newDec, evErr := u.evaluator.Execute(ctx, evaluate.Input{
		ScanRunID:            original.ScanRunID, // 017 §3.4: same scan
		Instance:             original.InstanceName,
		Sonarr:               inst.Client,
		Series:               seriesRow,
		Season:               season,
		Profile:              profile,
		MinCustomFormatScore: inst.Config.Search.MinCustomFormatScore,
		RequireAllAired:      inst.Config.Search.RequireAllAired,
		SkipSpecials:         inst.Config.Search.SkipSpecials,
		SkipAnime:            inst.Config.Search.SkipAnime,
		DryRun:               dryRun,
		Now:                  time.Now().UTC(),
		IgnoreCooldown:       true, // 017 §3.3
	})
	if evErr != nil {
		// Evaluator persists the error-decision row itself; supersede
		// original so the UI reflects the rescan click.
		if updErr := u.decisions.UpdateSupersededBy(ctx, original.ID, newDec.ID); updErr != nil {
			u.logger.WarnContext(ctx, "rescan_supersede_after_error_failed",
				slog.String("original_id", original.ID.String()),
				slog.String("new_id", newDec.ID.String()),
				slog.String("error", updErr.Error()))
		}
		return Output{NewDecision: newDec}, evErr
	}

	if err := u.decisions.UpdateSupersededBy(ctx, original.ID, newDec.ID); err != nil {
		u.logger.WarnContext(ctx, "rescan_supersede_failed",
			slog.String("original_id", original.ID.String()),
			slog.String("new_id", newDec.ID.String()),
			slog.String("error", err.Error()))
		return Output{NewDecision: newDec}, fmt.Errorf("supersede: %w", err)
	}

	u.logger.InfoContext(ctx, "rescan_succeeded",
		slog.String("original_id", original.ID.String()),
		slog.String("new_id", newDec.ID.String()),
		slog.String("instance", original.InstanceName),
		slog.Int("series_id", original.SeriesID),
		slog.Int("season", original.SeasonNumber),
		slog.String("new_outcome", string(newDec.Outcome)),
	)
	return Output{NewDecision: newDec}, nil
}

// checkNotExecuted enforces 017 §3.2 — same pattern as grab handler's
// findExistingGrab. 200-row cap.
func (u *UseCase) checkNotExecuted(ctx context.Context, d decision.Decision) error {
	if d.Selected == nil || d.Selected.Release.GUID == "" {
		return nil
	}
	inst := d.InstanceName
	sid := d.SeriesID
	season := d.SeasonNumber
	recs, _, err := u.grabs.List(ctx,
		ports.GrabFilter{Instance: &inst, SeriesID: &sid, SeasonNumber: &season},
		ports.Pagination{Limit: 200})
	if err != nil {
		return fmt.Errorf("lookup existing grab: %w", err)
	}
	for _, r := range recs {
		if r.ReleaseGUID == d.Selected.Release.GUID {
			return fmt.Errorf("%w: grab_id=%s", ErrAlreadyExecuted, r.ID)
		}
	}
	return nil
}
