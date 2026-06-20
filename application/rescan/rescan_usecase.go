// Package rescan implements story 017.
package rescan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/errtext"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

var (
	ErrAlreadySuperseded = errors.New("decision already superseded")
	ErrAlreadyExecuted   = errors.New("decision already executed against sonarr")
)

type UseCase struct {
	decisions       ports.DecisionRepository
	grabs           ports.GrabRepository
	scans           ports.ScanRepository
	inflight        scan.InflightController
	evaluator       *evaluate.UseCase
	instancesByName func() map[string]scan.Instance
	logger          *slog.Logger
}

func NewUseCase(decisions ports.DecisionRepository, grabs ports.GrabRepository,
	scans ports.ScanRepository, inflight scan.InflightController,
	evaluator *evaluate.UseCase, instancesByName func() map[string]scan.Instance,
	logger *slog.Logger) *UseCase {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "scan")
	}
	if instancesByName == nil {
		instancesByName = func() map[string]scan.Instance { return nil }
	}
	return &UseCase{
		decisions:       decisions,
		grabs:           grabs,
		scans:           scans,
		inflight:        inflight,
		evaluator:       evaluator,
		instancesByName: instancesByName,
		logger:          logger,
	}
}

type Input struct {
	DecisionID uuid.UUID
}

// StartResult is the synchronous return of Start. The goroutine reports
// terminal status by updating the ScanRecord; the handler maps this
// shape into one dto.ScanTriggerItem for the 202 response.
type StartResult struct {
	ScanRunID uuid.UUID
	Instance  domain.InstanceName
	Started   time.Time
	Status    string
}

// Start is the rescan entry point. The synchronous prelude validates,
// guards (not-superseded, not-executed), resolves the instance,
// acquires the per-instance single-flight lock, creates a fresh
// ScanRecord (trigger="rescan", status="running"), pre-applies the
// supersede pointer to the new decision id, then hands off to a
// goroutine that does the 4 Sonarr calls + evaluator + POST /release
// and finalises the scan record.
//
// On any goroutine error the supersede pointer is rolled back to NULL
// and the scan is finalised as `failed`.
func (u *UseCase) Start(ctx context.Context, in Input) (StartResult, error) {
	original, err := u.decisions.GetByID(ctx, in.DecisionID)
	if err != nil {
		return StartResult{}, fmt.Errorf("load decision: %w", err)
	}
	if original.SupersededByID != nil {
		return StartResult{}, fmt.Errorf("%w: %s", ErrAlreadySuperseded, original.ID)
	}
	if err := u.checkNotExecuted(ctx, original); err != nil {
		return StartResult{}, err
	}

	inst, ok := u.instancesByName()[string(original.InstanceName)]
	if !ok {
		return StartResult{}, fmt.Errorf("unknown instance: %s", original.InstanceName)
	}

	newScanID := uuid.New()
	if err := u.inflight.AcquireInstance(original.InstanceName, newScanID); err != nil {
		// Carry the instance name on the conflict so the handler can put
		// it on the 409 dto.ScanConflictResponse without re-loading.
		return StartResult{Instance: original.InstanceName},
			fmt.Errorf("acquire instance: %w", err)
	}

	newDecisionID := uuid.New()
	started := time.Now().UTC()
	rec := ports.ScanRecord{
		ID:           newScanID,
		InstanceName: original.InstanceName,
		Trigger:      string(scan.TriggerRescan),
		StartedAt:    started,
		Status:       "running",
		DryRun:       false, // rescan is never dry
	}
	// Detached writeCtx so a cancelled request ctx doesn't break the
	// scan-row insert; mirrors scan_usecase.go startOne.
	createCtx := logger.WithTraceID(context.Background(), newScanID.String())
	if err := u.scans.Create(createCtx, rec); err != nil {
		u.inflight.ReleaseInstance(original.InstanceName)
		return StartResult{}, fmt.Errorf("create scan record: %w", err)
	}

	if err := u.decisions.UpdateSupersededBy(ctx, original.ID, newDecisionID); err != nil {
		// Finalize the scan row we just created so it doesn't strand
		// at status="running"; mirror the detached writeCtx idiom
		// (see scan_usecase.go finalize* — request ctx may already be
		// cancelled while the writeCtx still must reach the DB).
		u.finalizeAsFailed(rec, started, fmt.Errorf("pre-apply supersede: %w", err))
		u.inflight.ReleaseInstance(original.InstanceName)
		return StartResult{}, fmt.Errorf("supersede: %w", err)
	}

	runCtx, cancel := context.WithCancel(logger.WithTraceID(context.Background(), newScanID.String()))
	u.inflight.SetInflightCancel(original.InstanceName, cancel)
	wg := u.inflight.BackgroundWG()
	if wg != nil {
		wg.Add(1)
	}

	u.logger.InfoContext(createCtx, "rescan_started",
		slog.String("original_id", original.ID.String()),
		slog.String("new_decision_id", newDecisionID.String()),
		slog.String("instance", string(original.InstanceName)),
		slog.String("scan_run_id", newScanID.String()),
		slog.Bool("async", true),
	)

	go func() {
		if wg != nil {
			defer wg.Done()
		}
		defer cancel()
		// Release LAST (LIFO order) so finalize writes land before the
		// inflight slot frees and a follow-up rescan can be accepted.
		defer u.inflight.ReleaseInstance(original.InstanceName)
		u.runDetached(runCtx, original, inst, rec, newDecisionID, started)
	}()

	return StartResult{
		ScanRunID: newScanID,
		Instance:  original.InstanceName,
		Started:   started,
		Status:    "running",
	}, nil
}

// runDetached is the goroutine body. On error it rolls back the
// supersede pointer and finalises the scan as `failed`; on success it
// finalises as `completed`.
func (u *UseCase) runDetached(ctx context.Context, original decision.Decision,
	inst scan.Instance, rec ports.ScanRecord, newDecisionID uuid.UUID, started time.Time) {
	seriesRow, ssErr := inst.Client.GetSeries(ctx, original.SeriesID)
	if ssErr != nil {
		u.failAndRollback(rec, original.ID, started, fmt.Errorf("get series: %w", ssErr))
		return
	}
	episodes, epErr := inst.Client.ListEpisodes(ctx, original.SeriesID, original.SeasonNumber)
	if epErr != nil {
		u.failAndRollback(rec, original.ID, started, fmt.Errorf("list episodes: %w", epErr))
		return
	}
	fileQuality, fqErr := inst.Client.ListEpisodeFiles(ctx, original.SeriesID)
	if fqErr != nil {
		u.failAndRollback(rec, original.ID, started, fmt.Errorf("list episode files: %w", fqErr))
		return
	}
	profile, prErr := inst.Client.GetQualityProfile(ctx, seriesRow.QualityProfile)
	if prErr != nil {
		u.failAndRollback(rec, original.ID, started, fmt.Errorf("get quality profile: %w", prErr))
		return
	}
	for i := range episodes {
		if q, ok := fileQuality[episodes[i].EpisodeFileID]; ok {
			episodes[i].QualityID = q
		}
	}
	season := series.Season{
		Number:    original.SeasonNumber,
		Monitored: true, // operator-explicit
		Episodes:  episodes,
	}

	newDec, evErr := u.evaluator.Execute(ctx, evaluate.Input{
		ScanRunID:            rec.ID, // Q1: new decision points at NEW scan_run_id
		Instance:             original.InstanceName,
		Sonarr:               inst.Client,
		Series:               seriesRow,
		Season:               season,
		Profile:              profile,
		MinCustomFormatScore: inst.Config.Search.MinCustomFormatScore,
		RequireAllAired:      inst.Config.Search.RequireAllAired,
		SkipSpecials:         inst.Config.Search.SkipSpecials,
		SkipAnime:            inst.Config.Search.SkipAnime,
		DryRun:               false,
		Now:                  time.Now().UTC(),
		IgnoreCooldown:       true, // 017 §3.3
		PreferredDecisionID:  &newDecisionID,
	})
	if evErr != nil {
		u.failAndRollback(rec, original.ID, started, evErr)
		return
	}

	u.finalizeAsCompleted(rec, started)
	u.logger.InfoContext(ctx, "rescan_succeeded",
		slog.String("original_id", original.ID.String()),
		slog.String("new_id", newDec.ID.String()),
		slog.String("instance", string(original.InstanceName)),
		slog.Int("series_id", int(original.SeriesID)),
		slog.Int("season", original.SeasonNumber),
		slog.String("new_outcome", string(newDec.Outcome)),
		slog.String("scan_run_id", rec.ID.String()),
	)
}

// failAndRollback writes the failed scan row and clears the
// pre-applied supersede pointer so the original looks live again.
// Detached writeCtx for the same reason scan_usecase.go uses one in
// finalize*: a cancelled request ctx must not block the DB writes.
func (u *UseCase) failAndRollback(rec ports.ScanRecord, originalID uuid.UUID, started time.Time, cause error) {
	u.finalizeAsFailed(rec, started, cause)
	writeCtx := logger.WithTraceID(context.Background(), rec.ID.String())
	if clrErr := u.decisions.ClearSupersededBy(writeCtx, originalID); clrErr != nil {
		u.logger.WarnContext(writeCtx, "rescan_supersede_rollback_failed",
			slog.String("original_id", originalID.String()),
			slog.String("error", clrErr.Error()))
	}
}

func (u *UseCase) finalizeAsFailed(rec ports.ScanRecord, started time.Time, cause error) {
	rec.Status = "failed"
	if cause != nil {
		// F-P2-4: cap at 4 KiB (errtext.MaxBytes).
		rec.ErrorMessage = errtext.Clamp(cause.Error())
	}
	finish := time.Now().UTC()
	rec.FinishedAt = &finish
	writeCtx := logger.WithTraceID(context.Background(), rec.ID.String())
	if err := u.scans.Update(writeCtx, rec); err != nil {
		u.logger.ErrorContext(writeCtx, "rescan_finalize_failed_update_failed",
			slog.String("scan_run_id", rec.ID.String()),
			slog.String("error", err.Error()))
	}
	u.logger.WarnContext(writeCtx, "rescan_failed",
		slog.String("scan_run_id", rec.ID.String()),
		slog.String("instance", string(rec.InstanceName)),
		slog.Float64("duration_seconds", finish.Sub(started).Seconds()),
		slog.String("error", rec.ErrorMessage),
	)
}

func (u *UseCase) finalizeAsCompleted(rec ports.ScanRecord, started time.Time) {
	rec.Status = "completed"
	finish := time.Now().UTC()
	rec.FinishedAt = &finish
	writeCtx := logger.WithTraceID(context.Background(), rec.ID.String())
	if err := u.scans.Update(writeCtx, rec); err != nil {
		u.logger.ErrorContext(writeCtx, "rescan_finalize_completed_update_failed",
			slog.String("scan_run_id", rec.ID.String()),
			slog.String("error", err.Error()))
	}
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
