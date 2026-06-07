package scan

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestDecidePrefilter is the pure routing-table unit. Asserts every
// permutation of (stats, flag) → (reason, skip) without touching any
// Sonarr stub or repo.
func TestDecidePrefilter(t *testing.T) {
	mkInst := func(flag bool) Instance {
		return Instance{Config: config.SonarrInstance{
			Name: "homelab", ScanSkipHandledSeasons: flag,
		}}
	}
	tests := []struct {
		name       string
		stats      series.SeasonStats
		flag       bool
		wantReason decision.Reason
		wantSkip   bool
	}{
		{"complete_unconditional", series.SeasonStats{Aired: 10, Existing: 10}, true, decision.ReasonAllComplete, true},
		{"complete_unconditional_even_flag_off", series.SeasonStats{Aired: 10, Existing: 10}, false, decision.ReasonAllComplete, true},
		{"sonarr_handles_flag_on", series.SeasonStats{Aired: 8, Existing: 0}, true, decision.ReasonSonarrHandles, true},
		{"sonarr_handles_flag_off_continues", series.SeasonStats{Aired: 8, Existing: 0}, false, "", false},
		{"partial_pack_continues", series.SeasonStats{Aired: 8, Existing: 3}, true, "", false},
		{"unaired_only_treated_complete", series.SeasonStats{Total: 10, Aired: 0, Existing: 0}, true, decision.ReasonAllComplete, true},
		{"clamp_negative_treated_complete", series.SeasonStats{Aired: 5, Existing: 8}, true, decision.ReasonAllComplete, true},
	}
	uc := &UseCase{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReason, gotSkip := uc.decidePrefilter(tt.stats, mkInst(tt.flag))
			assert.Equal(t, tt.wantSkip, gotSkip)
			assert.Equal(t, tt.wantReason, gotReason)
		})
	}
}

// TestPrefilterReasonLabel asserts the metric-label collapse.
func TestPrefilterReasonLabel(t *testing.T) {
	assert.Equal(t, "all_complete", prefilterReasonLabel(decision.ReasonAllComplete))
	assert.Equal(t, "sonarr_handles", prefilterReasonLabel(decision.ReasonSonarrHandles))
	// Fallback: any other reason returns the bare reason string. Not
	// expected on the metric path but documented as defensive.
	assert.Equal(t, "skip_no_missing_episodes", prefilterReasonLabel(decision.ReasonSkipNoMissing))
}

// prefilterSonarr is a fakeSonarr that increments per-method counters
// so the integration test can assert "zero SearchReleases / ListEpisodes
// calls for skipped seasons".
type prefilterSonarr struct {
	*fakeSonarr
	listEpisodesCalls   atomic.Int32
	searchReleasesCalls atomic.Int32
}

func (p *prefilterSonarr) ListEpisodes(ctx context.Context, sID, sn int) ([]series.Episode, error) {
	p.listEpisodesCalls.Add(1)
	return p.fakeSonarr.ListEpisodes(ctx, sID, sn)
}
func (p *prefilterSonarr) SearchReleases(ctx context.Context, sID, sn int) ([]release.Release, error) {
	p.searchReleasesCalls.Add(1)
	return p.fakeSonarr.SearchReleases(ctx, sID, sn)
}

// mkSeasonWithStats builds a Season carrying a populated Statistics
// block so decidePrefilter has something to route on. monitored=true so
// the loop doesn't trip on the unmonitored-season skip.
func mkSeasonWithStats(num int, total, aired, existing int) series.Season {
	return series.Season{
		Number:     num,
		Monitored:  true,
		Statistics: series.Statistics{Total: total, Aired: aired, EpisodeFileCount: existing},
	}
}

// buildUC wires a one-series, one-instance UseCase with the supplied
// flag value. Shared by the two integration tests.
func buildUC(t *testing.T, sonarr *prefilterSonarr, flag bool) (*UseCase, *fakeDecRepo) {
	t.Helper()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	decRepo := &fakeDecRepo{}
	evalUC := evaluate.NewUseCase(sonarr, decRepo, lg)
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name: "homelab", ScanSkipHandledSeasons: flag,
			Limits: config.LimitsConfig{ScanMaxSeries: 100, MaxGrabsPerScan: 10},
		},
		Client: sonarr,
	}}, evalUC, &fakeScanRepo{}, lg, true)
	return uc, decRepo
}

// TestScan_PrefilterSkipsCompleteAndSonarrHandles — PRD acceptance #3,
// #4, #6: mixed seasons with flag=true; partial-only ListEpisodes +
// SearchReleases; pre-filter Decisions carry season-stats snapshot.
func TestScan_PrefilterSkipsCompleteAndSonarrHandles(t *testing.T) {
	sonarr := &prefilterSonarr{fakeSonarr: &fakeSonarr{
		name: "homelab",
		series: []series.Series{{
			ID: 1, Title: "Show", Type: series.SeriesTypeStandard, Monitored: true, QualityProfile: 14,
			Seasons: []series.Season{
				mkSeasonWithStats(1, 10, 10, 10), // complete → all_complete
				mkSeasonWithStats(2, 12, 8, 0),   // sonarr_handles
				mkSeasonWithStats(3, 10, 8, 3),   // partial → evaluator
			},
		}},
	}}
	uc, decRepo := buildUC(t, sonarr, true)

	results, err := uc.Run(context.Background(), TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", results[0].Status)
	require.Len(t, decRepo.d, 3)

	byReason := map[decision.Reason]decision.Decision{}
	for _, d := range decRepo.d {
		byReason[d.Reason] = d
	}
	complete := byReason[decision.ReasonAllComplete]
	assert.Equal(t, decision.OutcomeSkip, complete.Outcome)
	assert.Equal(t, 10, complete.TotalEpisodes)
	assert.Equal(t, 10, complete.AiredEpisodes)
	assert.Equal(t, 10, complete.ExistingEpisodes)

	handled := byReason[decision.ReasonSonarrHandles]
	assert.Equal(t, decision.OutcomeSkip, handled.Outcome)
	assert.Equal(t, 12, handled.TotalEpisodes)
	assert.Equal(t, 8, handled.AiredEpisodes)
	assert.Equal(t, 0, handled.ExistingEpisodes)
	assert.Equal(t, 8, handled.MissingCount, "MissingCount = Aired-Existing")

	assert.EqualValues(t, 1, sonarr.listEpisodesCalls.Load())
	assert.EqualValues(t, 1, sonarr.searchReleasesCalls.Load())
}

// TestScan_PrefilterFlagOff — PRD #5. Flag=false: sonarr_handles routes
// to evaluator; all_complete still short-circuits (unconditional).
func TestScan_PrefilterFlagOff(t *testing.T) {
	sonarr := &prefilterSonarr{fakeSonarr: &fakeSonarr{
		name: "homelab",
		series: []series.Series{{
			ID: 1, Title: "Show", Type: series.SeriesTypeStandard, Monitored: true, QualityProfile: 14,
			Seasons: []series.Season{
				mkSeasonWithStats(1, 10, 10, 10), // complete (still skipped)
				mkSeasonWithStats(2, 10, 8, 0),   // sonarr_handles → evaluator
			},
		}},
	}}
	uc, decRepo := buildUC(t, sonarr, false)

	results, err := uc.Run(context.Background(), TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", results[0].Status)
	require.Len(t, decRepo.d, 2)

	var sawAllComplete, sawSonarrHandles bool
	for _, d := range decRepo.d {
		switch d.Reason {
		case decision.ReasonAllComplete:
			sawAllComplete = true
		case decision.ReasonSonarrHandles:
			sawSonarrHandles = true
		}
	}
	assert.True(t, sawAllComplete)
	assert.False(t, sawSonarrHandles)
	assert.EqualValues(t, 1, sonarr.listEpisodesCalls.Load())
	assert.EqualValues(t, 1, sonarr.searchReleasesCalls.Load())
}

var _ = time.Now // silence unused-import on future trim
