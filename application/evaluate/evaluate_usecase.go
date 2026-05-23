package evaluate

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

type Input struct {
	ScanRunID            uuid.UUID
	Instance             string
	Sonarr               ports.SonarrClient
	Series               series.Series
	Season               series.Season
	Profile              ports.QualityProfile
	OriginGUID           string
	OriginIndexerName    string
	OriginBonus          float64
	MinCustomFormatScore int
	RequireAllAired      bool
	SkipSpecials         bool
	SkipAnime            bool
	DryRun               bool
	Now                  time.Time
	// ExcludeGUIDs is a pre-built guid blacklist supplied by the caller.
	// When set, it is the authoritative exclude list. When nil and Cooldowns
	// is set, the evaluator queries the repository for active guid cooldowns.
	ExcludeGUIDs map[string]struct{}
	// Cooldowns lets the evaluator filter candidate guids against active
	// cooldown rows using the FilterActive(scope, keys, now) repo API
	// (D-2.1). Optional; nil means no DB-backed guid filtering.
	Cooldowns      ports.CooldownRepository
	IgnoreCooldown bool // 017 §3.3: rescan sets true; default false
}

type UseCase struct {
	sonarr    ports.SonarrClient
	decisions ports.DecisionRepository
	logger    *slog.Logger
}

func NewUseCase(sonarr ports.SonarrClient, decisions ports.DecisionRepository, logger *slog.Logger) *UseCase {
	return &UseCase{sonarr: sonarr, decisions: decisions, logger: logger}
}

func NewPerInstanceUseCase(decisions ports.DecisionRepository, logger *slog.Logger) *UseCase {
	return &UseCase{decisions: decisions, logger: logger}
}

func (u *UseCase) Execute(ctx context.Context, in Input) (decision.Decision, error) {
	d := decision.New(in.ScanRunID, in.Instance, in.Series.Title, in.Series.ID, in.Season.Number)

	if in.SkipSpecials && in.Season.Number == 0 {
		d.Outcome = decision.OutcomeSkip
		d.Reason = decision.ReasonSkipSpecials
		return u.finalize(ctx, d, in)
	}
	if in.SkipAnime && in.Series.Type == series.SeriesTypeAnime {
		d.Outcome = decision.OutcomeSkip
		d.Reason = decision.ReasonSkipAnime
		return u.finalize(ctx, d, in)
	}
	if !in.Season.Monitored {
		d.Outcome = decision.OutcomeSkip
		d.Reason = decision.ReasonSkipUnmonitoredSeason
		return u.finalize(ctx, d, in)
	}

	missing := in.Season.MissingNumbers()
	have := in.Season.Have()
	d.MissingCount = len(missing)
	d.ExistingCount = len(have)

	if len(missing) == 0 {
		d.Outcome = decision.OutcomeSkip
		d.Reason = decision.ReasonSkipNoMissing
		return u.finalize(ctx, d, in)
	}
	if len(have) == 0 {
		d.Outcome = decision.OutcomeSkip
		d.Reason = decision.ReasonSkipFullMissing
		return u.finalize(ctx, d, in)
	}

	client := in.Sonarr
	if client == nil {
		client = u.sonarr
	}
	releases, err := client.SearchReleases(ctx, in.Series.ID, in.Season.Number)
	if err != nil {
		d.Outcome = decision.OutcomeError
		d.Reason = decision.ReasonErrorFetchReleases
		// Capture the raw err string so operators see the actual
		// failure class on /decisions/:id without grepping logs.
		// Truncation + normalisation centralised in truncateErrorDetail.
		d.ErrorDetail = truncateErrorDetail(err.Error())
		_ = u.persist(ctx, d)
		return d, fmt.Errorf("search releases series=%d season=%d: %w", in.Series.ID, in.Season.Number, err)
	}
	d.ReleasesFound = len(releases)

	if len(releases) == 0 {
		d.Outcome = decision.OutcomeSkip
		d.Reason = decision.ReasonSkipNoReleases
		return u.finalize(ctx, d, in)
	}

	excludeGUIDs := in.ExcludeGUIDs
	if !in.IgnoreCooldown && excludeGUIDs == nil && in.Cooldowns != nil && len(releases) > 0 {
		keys := make([]string, 0, len(releases))
		for _, r := range releases {
			if r.GUID != "" {
				keys = append(keys, r.GUID)
			}
		}
		if len(keys) > 0 {
			active, cdErr := in.Cooldowns.FilterActive(ctx, cooldown.ScopeGUID, keys, in.Now)
			if cdErr != nil {
				u.logger.WarnContext(ctx, "guid cooldown lookup failed",
					slog.String("instance", in.Instance),
					slog.Int("series_id", in.Series.ID),
					slog.String("error", cdErr.Error()),
				)
			} else if len(active) > 0 {
				excludeGUIDs = make(map[string]struct{}, len(active))
				for _, c := range active {
					excludeGUIDs[c.Key] = struct{}{}
				}
			}
		}
	}

	filterRes := Filter(FilterInput{
		Releases:             releases,
		Missing:              missing,
		Have:                 have,
		Episodes:             in.Season.Episodes,
		Profile:              in.Profile,
		MinCustomFormatScore: in.MinCustomFormatScore,
		RequireAllAired:      in.RequireAllAired,
		NowUTC:               in.Now,
		ExcludeGUIDs:         excludeGUIDs,
	})
	d.FilteredOut = filterRes.FilteredOut
	d.CandidatesCount = len(filterRes.Kept)

	observability.ObserveCandidatesFound(in.Instance, len(filterRes.Kept))

	if len(filterRes.Kept) == 0 {
		d.Outcome = decision.OutcomeSkip
		d.Reason = decision.ReasonSkipNoCandidates
		return u.finalize(ctx, d, in)
	}

	scored := Rank(RankInput{
		Releases:          filterRes.Kept,
		Missing:           missing,
		OriginGUID:        in.OriginGUID,
		OriginIndexerName: in.OriginIndexerName,
		OriginBonus:       in.OriginBonus,
	})
	best := scored[0]
	d.Selected = &best
	observability.ObserveCoverageCount(in.Instance, best.Coverage)

	if in.DryRun {
		d.Outcome = decision.OutcomeGrab
		d.Reason = decision.ReasonGrabSelectedDryRun
		d.DryRunWouldGrab = true
	} else {
		// Real-grab path. DryRunWouldGrab stays false — the grab record in
		// grab_records (status=grabbed or grab_failed) is the real audit.
		d.Outcome = decision.OutcomeGrab
		d.Reason = decision.ReasonGrabSelected
		d.DryRunWouldGrab = false
	}
	return u.finalize(ctx, d, in)
}

func (u *UseCase) finalize(ctx context.Context, d decision.Decision, in Input) (decision.Decision, error) {
	u.emitLog(ctx, d, in)
	observability.SeriesEvaluated(in.Instance, string(d.Outcome))
	if err := u.persist(ctx, d); err != nil {
		return d, err
	}
	return d, nil
}

func (u *UseCase) persist(ctx context.Context, d decision.Decision) error {
	if u.decisions == nil {
		return nil
	}
	if err := u.decisions.Save(ctx, d); err != nil {
		return fmt.Errorf("persist decision: %w", err)
	}
	return nil
}

func (u *UseCase) emitLog(ctx context.Context, d decision.Decision, in Input) {
	attrs := []any{
		slog.String("scan_run_id", d.ScanRunID.String()),
		slog.String("instance", d.InstanceName),
		slog.Int("series_id", d.SeriesID),
		slog.String("series_title", d.SeriesTitle),
		slog.Int("season_number", d.SeasonNumber),
		slog.Int("missing_count", d.MissingCount),
		slog.Int("existing_count", d.ExistingCount),
		slog.Int("releases_found", d.ReleasesFound),
		slog.Int("after_filter", d.CandidatesCount),
		slog.String("decision", string(d.Outcome)),
		slog.String("reason", string(d.Reason)),
		slog.Bool("dry_run", in.DryRun),
		slog.Bool("dry_run_would_grab", d.DryRunWouldGrab),
	}

	if d.Selected != nil {
		attrs = append(attrs, slog.Group("selected",
			slog.String("guid", d.Selected.Release.GUID),
			slog.String("indexer", d.Selected.Release.IndexerName),
			slog.Int("indexer_priority", d.Selected.Release.IndexerPriority),
			slog.String("release_title", d.Selected.Release.Title),
			slog.String("quality", d.Selected.Release.QualityName),
			slog.Int("custom_format_score", d.Selected.Release.CustomFormatScore),
			slog.Int("coverage", d.Selected.Coverage),
			slog.Int("seeders", d.Selected.Release.Seeders),
			slog.Int64("size_bytes", d.Selected.Release.SizeBytes),
			slog.Bool("is_origin_release", d.Selected.IsOriginRelease),
		))
		attrs = append(attrs, slog.Int("alternatives_considered", maxAlt(d.CandidatesCount)))
	}

	if len(d.FilteredOut) > 0 && len(d.FilteredOut) <= 25 {
		filtered := make([]any, 0, len(d.FilteredOut))
		for _, fc := range d.FilteredOut {
			filtered = append(filtered, slog.Group("",
				slog.String("guid", fc.GUID),
				slog.String("reason", fc.Reason),
				slog.String("indexer", fc.Indexer),
				slog.String("quality", fc.Quality),
				slog.Int("coverage", fc.Coverage),
			))
		}
		attrs = append(attrs, slog.Any("filtered_out", filtered))
	}

	level := slog.LevelInfo
	if d.Outcome == decision.OutcomeError {
		level = slog.LevelError
	}
	u.logger.Log(ctx, level, "season_evaluated", attrs...)
}

func maxAlt(total int) int {
	if total <= 0 {
		return 0
	}
	return total - 1
}
