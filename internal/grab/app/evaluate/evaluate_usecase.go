package evaluate

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
)

type Input struct {
	ScanRunID            uuid.UUID
	Instance             domain.InstanceName
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
	// PreferredDecisionID, when non-nil, overrides the freshly-allocated
	// decision UUID. The async rescan path pre-allocates the id so it
	// can wire the supersede pointer before the goroutine writes the
	// new decision row. nil = use decision.New's default (uuid.New()).
	PreferredDecisionID *uuid.UUID
	// IntentBecauseOverride, when non-empty, overrides the
	// auto-classified ChosenBecause (091a / F-P2-2). The replay
	// (Watchdog) and manual-grab paths use this to stamp their
	// reason directly on the persisted Decision row — the evaluator
	// has no other way to know "this came from a replay" vs a fresh
	// auto-scan. The free-text amplification rides along in
	// IntentDetailOverride. Both empty = use the auto classifier.
	IntentBecauseOverride decision.ChosenBecause
	IntentDetailOverride  string
}

type UseCase struct {
	sonarr    ports.SonarrClient
	decisions ports.DecisionRepository
	grabs     ports.GrabRepository // 046a: optional, for GrabbedEpisodes counter
	logger    *slog.Logger
}

func NewUseCase(sonarr ports.SonarrClient, decisions ports.DecisionRepository, logger *slog.Logger) *UseCase {
	return &UseCase{sonarr: sonarr, decisions: decisions, logger: logger}
}

func NewPerInstanceUseCase(decisions ports.DecisionRepository, logger *slog.Logger) *UseCase {
	return &UseCase{decisions: decisions, logger: logger}
}

// WithGrabRepository attaches a GrabRepository so the evaluator can
// snapshot the GrabbedEpisodes counter onto every Decision. Optional —
// when nil, GrabbedEpisodes stays at 0 (degrades cleanly on the UI).
// Existing callers that don't wire this won't see a behaviour change.
func (u *UseCase) WithGrabRepository(g ports.GrabRepository) *UseCase {
	u.grabs = g
	return u
}

func (u *UseCase) Execute(ctx context.Context, in Input) (decision.Decision, error) {
	d := decision.New(in.ScanRunID, in.Instance, in.Series.Title, in.Series.ID, in.Season.Number)
	if in.PreferredDecisionID != nil {
		d.ID = *in.PreferredDecisionID
	}

	// 046a season-stats snapshot. Populate BEFORE any early-skip path
	// so all decision rows — even the skip_specials / skip_unmonitored
	// branches — carry the counter snapshot. Grabbed count is fetched
	// lazily at finalize time (one SQL per persisted row).
	stats := series.SeasonStatsFromStatistics(in.Season.Statistics)
	d.TotalEpisodes = stats.Total
	d.AiredEpisodes = stats.Aired
	d.ExistingEpisodes = stats.Existing

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
					slog.String("instance", string(in.Instance)),
					slog.Int("series_id", int(in.Series.ID)),
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

	// 091a / F-P2-2: capture the grab intent — why this candidate
	// beat its alternatives. Two paths in the auto-scan default:
	//   - len(scored)==1 → ChosenBecauseOnlyCandidate (no comparison)
	//   - len(scored)>1  → ChosenBecauseHighestScore + detail string
	// `had` is the per-episode HaveNumbers() from the same call site
	// that produced `missing`, so the operator-visible target / had
	// pair matches the evaluator's view at decide-time. Replay
	// (Watchdog) and manual paths supply IntentBecauseOverride +
	// IntentDetailOverride so the persisted row carries their
	// reason instead of the auto classifier's output.
	because, detail := classifyAutoIntent(scored)
	if in.IntentBecauseOverride != "" {
		because = in.IntentBecauseOverride
		detail = in.IntentDetailOverride
	}
	intent := decision.NewIntent(missing, in.Season.HaveNumbers(), because, detail)
	d.Intent = &intent

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

// classifyAutoIntent reports the ChosenBecause + free-text detail for
// a successful auto-pick (the only path that has multiple candidates
// to compare). Replay / manual paths supply their own Intent in the
// caller — this helper is the scan-path default.
//
//   - len(scored)==0: defensive nil; Execute never reaches this with
//     an empty slice (the no-candidates branch returned earlier), so
//     the empty enum is fine.
//   - len(scored)==1: ChosenBecauseOnlyCandidate, detail is the lone
//     CFS so the SPA can render the absolute score even without
//     comparison.
//   - len(scored)>=2: ChosenBecauseHighestScore. Detail enumerates
//     up to 3 alternates' CFS so the operator sees the gap that
//     drove the pick.
func classifyAutoIntent(scored []release.Scored) (decision.ChosenBecause, string) {
	switch len(scored) {
	case 0:
		return decision.ChosenBecause(""), ""
	case 1:
		return decision.ChosenBecauseOnlyCandidate,
			fmt.Sprintf("score %d", scored[0].Release.CustomFormatScore)
	default:
	}
	const maxAlternates = 3
	bestScore := scored[0].Release.CustomFormatScore
	alts := make([]string, 0, maxAlternates)
	for i := 1; i < len(scored) && i <= maxAlternates; i++ {
		alts = append(alts, fmt.Sprintf("%d", scored[i].Release.CustomFormatScore))
	}
	more := ""
	if len(scored)-1 > maxAlternates {
		more = fmt.Sprintf(" (+%d more)", len(scored)-1-maxAlternates)
	}
	detail := fmt.Sprintf("score %d vs alternates %s%s",
		bestScore, strings.Join(alts, ", "), more)
	return decision.ChosenBecauseHighestScore, detail
}

func (u *UseCase) finalize(ctx context.Context, d decision.Decision, in Input) (decision.Decision, error) {
	// 046a — best-effort fetch of GrabbedEpisodes. Failure logs WARN
	// and proceeds with GrabbedEpisodes=0 (the UI placeholder); a flaky
	// DB read here is NOT a reason to fail the entire decision write.
	if u.grabs != nil {
		grabbed, gerr := u.grabs.CountImportedEpisodes(ctx, in.Instance, in.Series.ID, in.Season.Number)
		if gerr != nil {
			u.logger.WarnContext(ctx, "count_imported_episodes_failed",
				slog.String("instance", string(in.Instance)),
				slog.Int("series_id", int(in.Series.ID)),
				slog.Int("season_number", in.Season.Number),
				slog.String("error", gerr.Error()))
		} else {
			d.GrabbedEpisodes = grabbed
		}
	}
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
		slog.String("instance", string(d.InstanceName)),
		slog.Int("series_id", int(d.SeriesID)),
		slog.String("series_title", d.SeriesTitle),
		slog.Int("season_number", d.SeasonNumber),
		slog.Int("missing_count", d.MissingCount),
		slog.Int("existing_count", d.ExistingCount),
		slog.Int("releases_found", d.ReleasesFound),
		slog.Int("after_filter", d.CandidatesCount),
		slog.Int("total_episodes", d.TotalEpisodes),
		slog.Int("aired_episodes", d.AiredEpisodes),
		slog.Int("existing_episodes", d.ExistingEpisodes),
		slog.Int("grabbed_episodes", d.GrabbedEpisodes),
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

// RecordSkip persists a synthetic skip Decision built from the supplied
// SeasonStats — used by the 046b scan pre-filter to short-circuit a
// season without calling SearchReleases / ListEpisodes. The Decision
// carries the stats snapshot on TotalEpisodes/AiredEpisodes/ExistingEpisodes
// (and GrabbedEpisodes fetched lazily by finalize, just like Execute).
//
// reason MUST be a skip-class Reason (e.g. decision.ReasonAllComplete or
// decision.ReasonSonarrHandles); callers that violate this still get a
// Decision row but its Category will resolve to something other than
// all_complete / sonarr_handles, which is harmless but defeats the F7
// UI grouping.
//
// Input must carry ScanRunID + Instance + Series + Season; everything
// else is optional. PreferredDecisionID is honoured the same way Execute
// does (no current 046b caller uses it, but the symmetry keeps the API
// predictable for future supersede-aware code paths).
func (u *UseCase) RecordSkip(ctx context.Context, in Input, reason decision.Reason, stats series.SeasonStats) (decision.Decision, error) {
	d := decision.New(in.ScanRunID, in.Instance, in.Series.Title, in.Series.ID, in.Season.Number)
	if in.PreferredDecisionID != nil {
		d.ID = *in.PreferredDecisionID
	}
	d.Outcome = decision.OutcomeSkip
	d.Reason = reason
	d.TotalEpisodes = stats.Total
	d.AiredEpisodes = stats.Aired
	d.ExistingEpisodes = stats.Existing
	// MissingCount mirrors the partial-pack count so the legacy field
	// stays consistent with the new ExistingEpisodes field — UI consumers
	// that still read MissingCount get a non-zero figure for sonarr_handles
	// rows. ExistingCount stays at 0 because the per-episode `have` slice
	// isn't computed on the pre-filter path (no ListEpisodes call).
	d.MissingCount = stats.Missing()
	return u.finalize(ctx, d, in)
}
