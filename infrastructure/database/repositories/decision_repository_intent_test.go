package repositories

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
)

// TestDecisionRepository_SaveAndGet_RoundTripsIntent — 091a / F-P2-2.
// Save → GetByID returns the same Intent shape, including empty slices
// and the chosen_because enum.
func TestDecisionRepository_SaveAndGet_RoundTripsIntent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)
	ctx := context.Background()

	d := decision.New(uuid.New(), "main", "Severance", 122, 2)
	d.Outcome = decision.OutcomeGrab
	d.Reason = decision.ReasonGrabSelectedDryRun
	d.DryRunWouldGrab = true
	intent := decision.NewIntent(
		[]int{10, 11},
		[]int{1, 2, 3, 4, 5, 6, 7, 8, 9},
		decision.ChosenBecauseHighestScore,
		"score 88 vs alternates 64, 71",
	)
	d.Intent = &intent

	require.NoError(t, repo.Save(ctx, d))

	got, err := repo.GetByID(ctx, d.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Intent, "Intent must round-trip")
	assert.Equal(t, []int{10, 11}, got.Intent.TargetEpisodes)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, got.Intent.HadEpisodes)
	assert.Equal(t, decision.ChosenBecauseHighestScore, got.Intent.ChosenBecause)
	assert.Equal(t, "score 88 vs alternates 64, 71", got.Intent.ChosenReasonDetail)
}

// TestDecisionRepository_SaveAndGet_NilIntentStaysNil — back-compat
// guarantee. Pre-091a rows have NULL intent; the constructor never
// sets it. Save → GetByID must keep Intent == nil.
func TestDecisionRepository_SaveAndGet_NilIntentStaysNil(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)
	ctx := context.Background()

	d := decision.New(uuid.New(), "main", "Severance", 122, 2)
	d.Outcome = decision.OutcomeSkip
	d.Reason = decision.ReasonSkipNoMissing

	require.NoError(t, repo.Save(ctx, d))

	got, err := repo.GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Intent, "nil Intent must stay nil after persistence")
}

// TestDecisionRepository_UpdateIntent — 091a / F-P2-2 refine path
// used by the regrab use case to promote the watchdog placeholder
// once the candidate's quality is in hand.
func TestDecisionRepository_UpdateIntent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)
	ctx := context.Background()

	d := decision.New(uuid.New(), "main", "Severance", 122, 2)
	d.Outcome = decision.OutcomeGrab
	d.Reason = decision.ReasonGrabSelected
	placeholder := decision.NewIntent([]int{5}, []int{1, 2, 3, 4},
		decision.ChosenBecauseWatchdogBetterOther, "placeholder")
	d.Intent = &placeholder
	require.NoError(t, repo.Save(ctx, d))

	refined := decision.NewIntent([]int{5}, []int{1, 2, 3, 4},
		decision.ChosenBecauseWatchdogBetterQuality,
		"WEBDL-2160p beats WEBDL-1080p")
	require.NoError(t, repo.UpdateIntent(ctx, d.ID, &refined))

	got, err := repo.GetByID(ctx, d.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Intent)
	assert.Equal(t, decision.ChosenBecauseWatchdogBetterQuality, got.Intent.ChosenBecause)
	assert.Equal(t, "WEBDL-2160p beats WEBDL-1080p", got.Intent.ChosenReasonDetail)
}

// TestDecisionRepository_UpdateIntent_UnknownID — ports.ErrNotFound
// when the target row doesn't exist.
func TestDecisionRepository_UpdateIntent_UnknownID(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)
	ctx := context.Background()

	intent := decision.NewIntent(nil, nil, decision.ChosenBecauseHighestScore, "x")
	err := repo.UpdateIntent(ctx, uuid.New(), &intent)
	assert.ErrorIs(t, err, ports.ErrNotFound)
}
