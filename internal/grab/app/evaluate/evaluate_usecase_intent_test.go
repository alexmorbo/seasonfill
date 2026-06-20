package evaluate

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// TestExecute_Intent_HighestScore — two candidates, evaluator picks
// the higher CFS one and stamps ChosenBecauseHighestScore + a detail
// listing the score gap.
func TestExecute_Intent_HighestScore(t *testing.T) {
	t.Parallel()
	stub := &stubSonarr{releases: []release.Release{
		{
			GUID: "g1", Title: "Best Pack", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "RT", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 88, Rejections: []string{"Full season pack"},
		},
		{
			GUID: "g2", Title: "Mid Pack", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "KZ", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 64, Rejections: []string{"Full season pack"},
		},
	}}
	uc := NewUseCase(stub, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "x",
		Series:  series.Series{ID: 1, Title: "S", Type: series.SeriesTypeStandard, Monitored: true},
		Season:  makeSeason([]int{4, 5}, []int{1, 2, 3}),
		Profile: ports.QualityProfile{Items: []ports.QualityItem{{ID: 19, Order: 9}}},
		Sonarr:  stub,
		DryRun:  true,
		Now:     time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotNil(t, d.Intent, "Intent must be populated on grab path")
	assert.Equal(t, decision.ChosenBecauseHighestScore, d.Intent.ChosenBecause)
	assert.Contains(t, d.Intent.ChosenReasonDetail, "score 88")
	assert.Contains(t, d.Intent.ChosenReasonDetail, "64")
	assert.Equal(t, []int{4, 5}, d.Intent.TargetEpisodes)
	assert.Equal(t, []int{1, 2, 3}, d.Intent.HadEpisodes)
}

// TestExecute_Intent_OnlyCandidate — single candidate stamps
// ChosenBecauseOnlyCandidate.
func TestExecute_Intent_OnlyCandidate(t *testing.T) {
	t.Parallel()
	stub := &stubSonarr{releases: []release.Release{
		{
			GUID: "g1", Title: "Lone Pack", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "RT", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 42, Rejections: []string{"Full season pack"},
		},
	}}
	uc := NewUseCase(stub, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "x",
		Series:  series.Series{ID: 1, Title: "S", Type: series.SeriesTypeStandard, Monitored: true},
		Season:  makeSeason([]int{4, 5}, []int{1, 2, 3}),
		Profile: ports.QualityProfile{Items: []ports.QualityItem{{ID: 19, Order: 9}}},
		Sonarr:  stub,
		DryRun:  true,
		Now:     time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotNil(t, d.Intent)
	assert.Equal(t, decision.ChosenBecauseOnlyCandidate, d.Intent.ChosenBecause)
	assert.Contains(t, d.Intent.ChosenReasonDetail, "score 42")
}

// TestExecute_Intent_ReplayOverride — when the Input carries a
// IntentBecauseOverride (the regrab/watchdog path), it wins over the
// auto classifier.
func TestExecute_Intent_ReplayOverride(t *testing.T) {
	t.Parallel()
	stub := &stubSonarr{releases: []release.Release{
		{
			GUID: "g1", Title: "Better Pack", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "RT", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 88, Rejections: []string{"Full season pack"},
		},
		{
			GUID: "g2", Title: "Mid Pack", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "KZ", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 64, Rejections: []string{"Full season pack"},
		},
	}}
	uc := NewUseCase(stub, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "x",
		Series:                series.Series{ID: 1, Title: "S", Type: series.SeriesTypeStandard, Monitored: true},
		Season:                makeSeason([]int{4, 5}, []int{1, 2, 3}),
		Profile:               ports.QualityProfile{Items: []ports.QualityItem{{ID: 19, Order: 9}}},
		Sonarr:                stub,
		DryRun:                true,
		Now:                   time.Now().UTC(),
		IntentBecauseOverride: decision.ChosenBecauseWatchdogBetterQuality,
		IntentDetailOverride:  "Watchdog re-grab: 1080p → 2160p",
	})
	require.NoError(t, err)
	require.NotNil(t, d.Intent)
	assert.Equal(t, decision.ChosenBecauseWatchdogBetterQuality, d.Intent.ChosenBecause)
	assert.Equal(t, "Watchdog re-grab: 1080p → 2160p", d.Intent.ChosenReasonDetail)
	// target / had still match the season
	assert.Equal(t, []int{4, 5}, d.Intent.TargetEpisodes)
}

// TestExecute_Intent_NilOnSkipPath — synthetic skip rows do NOT carry
// intent. Pre-091a parity for the skip branch.
func TestExecute_Intent_NilOnSkipPath(t *testing.T) {
	t.Parallel()
	uc := NewUseCase(&stubSonarr{}, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID:    uuid.New(),
		Instance:     "x",
		Series:       series.Series{ID: 1, Monitored: true, Type: series.SeriesTypeStandard},
		Season:       series.Season{Number: 0, Monitored: true},
		SkipSpecials: true,
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeSkip, d.Outcome)
	assert.Nil(t, d.Intent, "skip-path decisions must carry no intent")
}

// TestClassifyAutoIntent_DetailFormat covers the helper in isolation
// so the wire-facing detail string can be regression-locked without
// re-running the whole evaluator.
func TestClassifyAutoIntent_DetailFormat(t *testing.T) {
	t.Parallel()

	scored := []release.Scored{
		{Release: release.Release{CustomFormatScore: 88}},
		{Release: release.Release{CustomFormatScore: 64}},
		{Release: release.Release{CustomFormatScore: 50}},
	}
	because, detail := classifyAutoIntent(scored)
	assert.Equal(t, decision.ChosenBecauseHighestScore, because)
	assert.True(t, strings.HasPrefix(detail, "score 88"),
		"detail must lead with the winning score; got %q", detail)
	assert.Contains(t, detail, "64")
	assert.Contains(t, detail, "50")

	// Single candidate path.
	one := []release.Scored{{Release: release.Release{CustomFormatScore: 33}}}
	because, detail = classifyAutoIntent(one)
	assert.Equal(t, decision.ChosenBecauseOnlyCandidate, because)
	assert.Equal(t, "score 33", detail)

	// Many alternates — the "+N more" suffix kicks in.
	many := make([]release.Scored, 6)
	for i := range many {
		many[i] = release.Scored{Release: release.Release{CustomFormatScore: 100 - i*10}}
	}
	_, detail = classifyAutoIntent(many)
	assert.Contains(t, detail, "+2 more")
}
