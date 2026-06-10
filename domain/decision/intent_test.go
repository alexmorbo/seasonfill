package decision

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChosenBecause_IsValid(t *testing.T) {
	t.Parallel()

	for _, v := range []ChosenBecause{
		ChosenBecauseOnlyCandidate,
		ChosenBecauseHighestScore,
		ChosenBecauseFirstPassQuality,
		ChosenBecauseWatchdogBetterQuality,
		ChosenBecauseWatchdogBetterDub,
		ChosenBecauseWatchdogBetterOther,
		ChosenBecauseWatchdogReplayUnregistered,
		ChosenBecauseManualSelection,
	} {
		assert.True(t, v.IsValid(), "%s must be valid", v)
	}
	for _, v := range []ChosenBecause{"", "future_axis", "typo"} {
		assert.False(t, v.IsValid(), "%q must be invalid", string(v))
	}
}

func TestNewIntent_CopiesSlices(t *testing.T) {
	t.Parallel()

	target := []int{1, 2, 3}
	had := []int{10, 11}
	in := NewIntent(target, had, ChosenBecauseHighestScore, "score 88 vs 64")

	// Mutate originals to assert the constructor took defensive copies.
	target[0] = 999
	had[0] = 999

	assert.Equal(t, []int{1, 2, 3}, in.TargetEpisodes)
	assert.Equal(t, []int{10, 11}, in.HadEpisodes)
	assert.Equal(t, ChosenBecauseHighestScore, in.ChosenBecause)
	assert.Equal(t, "score 88 vs 64", in.ChosenReasonDetail)
}

func TestNewIntent_NilInputs(t *testing.T) {
	t.Parallel()

	in := NewIntent(nil, nil, ChosenBecauseOnlyCandidate, "score 50")
	assert.NotNil(t, in.TargetEpisodes, "TargetEpisodes must never be nil")
	assert.NotNil(t, in.HadEpisodes, "HadEpisodes must never be nil")
	assert.Empty(t, in.TargetEpisodes)
	assert.Empty(t, in.HadEpisodes)
}

func TestIntent_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	in := NewIntent([]int{5, 6}, []int{1, 2, 3, 4}, ChosenBecauseHighestScore, "score 88 vs 64, 71")
	raw, err := json.Marshal(in)
	assert.NoError(t, err)

	var got Intent
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, in.TargetEpisodes, got.TargetEpisodes)
	assert.Equal(t, in.HadEpisodes, got.HadEpisodes)
	assert.Equal(t, in.ChosenBecause, got.ChosenBecause)
	assert.Equal(t, in.ChosenReasonDetail, got.ChosenReasonDetail)
}

func TestNewIntent_AcceptsUnknownChosenBecause(t *testing.T) {
	t.Parallel()
	// Constructor must NOT reject unknown values — the value comes
	// from runtime data and forward-compat depends on it being stored
	// verbatim.
	in := NewIntent([]int{1}, []int{}, ChosenBecause("future_axis"), "experimental")
	assert.Equal(t, ChosenBecause("future_axis"), in.ChosenBecause)
}

func TestChosenBecause_IsValid_ReplayUnregistered(t *testing.T) {
	t.Parallel()
	assert.True(t, ChosenBecauseWatchdogReplayUnregistered.IsValid(),
		"watchdog_replay_unregistered must be IsValid()=true")
	assert.Equal(t, "watchdog_replay_unregistered",
		string(ChosenBecauseWatchdogReplayUnregistered),
		"wire string is frozen — the SPA reason map keys on it")
}
