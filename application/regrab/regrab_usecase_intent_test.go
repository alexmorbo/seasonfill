package regrab

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/release"
	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
)

// TestRefineWatchdogIntent_Quality — when the selected release's
// resolution outranks the original grab's, promote the placeholder
// to ChosenBecauseWatchdogBetterQuality. 091a / F-P2-2.
func TestRefineWatchdogIntent_Quality(t *testing.T) {
	t.Parallel()
	origGrab := domaingrab.Record{
		ID:      uuid.New(),
		Quality: "WEBDL-1080p",
	}
	intent := decision.NewIntent(
		[]int{1, 2}, []int{3, 4},
		decision.ChosenBecauseWatchdogBetterOther,
		"placeholder",
	)
	d := decision.Decision{
		ID:      uuid.New(),
		Outcome: decision.OutcomeGrab,
		Selected: &release.Scored{Release: release.Release{
			QualityName: "WEBDL-2160p",
		}},
		Intent: &intent,
	}

	got, ok := refineWatchdogIntent(d, origGrab)
	require.True(t, ok, "1080p → 2160p must promote to BetterQuality")
	assert.Equal(t, decision.ChosenBecauseWatchdogBetterQuality, got.ChosenBecause)
	assert.Contains(t, got.ChosenReasonDetail, "WEBDL-2160p")
	assert.Contains(t, got.ChosenReasonDetail, "WEBDL-1080p")
}

// TestRefineWatchdogIntent_EqualQuality — same resolution stays at
// the placeholder; the frontend's grab-pair derivation handles dub +
// other axes.
func TestRefineWatchdogIntent_EqualQuality(t *testing.T) {
	t.Parallel()
	origGrab := domaingrab.Record{ID: uuid.New(), Quality: "WEBDL-1080p"}
	intent := decision.NewIntent(nil, nil, decision.ChosenBecauseWatchdogBetterOther, "placeholder")
	d := decision.Decision{
		Outcome: decision.OutcomeGrab,
		Intent:  &intent,
		Selected: &release.Scored{Release: release.Release{
			QualityName: "WEBDL-1080p",
		}},
	}
	_, ok := refineWatchdogIntent(d, origGrab)
	assert.False(t, ok)
}

// TestRefineWatchdogIntent_MissingData — empty quality strings on
// either side means we can't compare; stay on the placeholder.
func TestRefineWatchdogIntent_MissingData(t *testing.T) {
	t.Parallel()
	intent := decision.NewIntent(nil, nil, decision.ChosenBecauseWatchdogBetterOther, "placeholder")
	d := decision.Decision{
		Outcome:  decision.OutcomeGrab,
		Intent:   &intent,
		Selected: &release.Scored{Release: release.Release{QualityName: "WEBDL-2160p"}},
	}
	_, ok := refineWatchdogIntent(d, domaingrab.Record{ID: uuid.New(), Quality: ""})
	assert.False(t, ok, "missing origQuality must NOT refine")

	d.Selected = &release.Scored{Release: release.Release{QualityName: ""}}
	_, ok = refineWatchdogIntent(d, domaingrab.Record{ID: uuid.New(), Quality: "WEBDL-1080p"})
	assert.False(t, ok, "missing newQuality must NOT refine")
}

// TestRefineWatchdogIntent_NoSelected — defensive guard.
func TestRefineWatchdogIntent_NoSelected(t *testing.T) {
	t.Parallel()
	intent := decision.NewIntent(nil, nil, decision.ChosenBecauseWatchdogBetterOther, "placeholder")
	d := decision.Decision{
		Outcome:  decision.OutcomeGrab,
		Intent:   &intent,
		Selected: nil,
	}
	_, ok := refineWatchdogIntent(d, domaingrab.Record{ID: uuid.New(), Quality: "WEBDL-1080p"})
	assert.False(t, ok)
}

// TestWatchdogQualityRank — sanity check on the resolution mapping.
func TestWatchdogQualityRank(t *testing.T) {
	t.Parallel()
	assert.Greater(t, watchdogQualityRank("WEBDL-2160p"), watchdogQualityRank("WEBDL-1080p"))
	assert.Greater(t, watchdogQualityRank("WEBDL-1080p"), watchdogQualityRank("WEBDL-720p"))
	assert.Equal(t, 0, watchdogQualityRank(""))
	assert.Equal(t, 0, watchdogQualityRank("Unknown"))
}
