package repositories

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

func TestDecisionRepository_Save_NoSelected(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)

	d := decision.New(uuid.New(), "main", "Hijack", 122, 2)
	d.Outcome = decision.OutcomeSkip
	d.Reason = decision.ReasonSkipNoMissing
	d.MissingCount = 0
	d.ExistingCount = 8
	d.FilteredOut = []decision.FilteredCandidate{
		{GUID: "x", Title: "stub", Indexer: "RT", Reason: "test", Coverage: 0},
	}

	require.NoError(t, repo.Save(context.Background(), d))

	var model database.DecisionModel
	require.NoError(t, db.First(&model, "id = ?", d.ID.String()).Error)
	assert.Equal(t, "skip", model.Decision)
	assert.Equal(t, string(decision.ReasonSkipNoMissing), model.Reason)
	assert.Empty(t, model.SelectedGUID)
	assert.Nil(t, model.SelectedData)
	assert.NotEmpty(t, model.FilteredOut)
}

func TestDecisionRepository_Save_WithSelected(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)

	d := decision.New(uuid.New(), "main", "Hijack", 122, 2)
	d.Outcome = decision.OutcomeGrab
	d.Reason = decision.ReasonGrabSelectedDryRun
	d.WouldGrab = true
	d.CandidatesCount = 1
	d.ReleasesFound = 4
	d.Selected = &release.Scored{
		Release: release.Release{
			GUID:                 "g1",
			Title:                "S02 Pack",
			IndexerName:          "RT",
			MappedEpisodeNumbers: []int{1, 2, 3},
			PublishedUTC:         time.Now().UTC().Truncate(time.Second),
		},
		Coverage:        3,
		IsOriginRelease: true,
	}

	require.NoError(t, repo.Save(context.Background(), d))

	var model database.DecisionModel
	require.NoError(t, db.First(&model, "id = ?", d.ID.String()).Error)
	assert.Equal(t, "grab", model.Decision)
	assert.Equal(t, "g1", model.SelectedGUID)
	require.NotNil(t, model.SelectedData)

	var roundTrip release.Scored
	require.NoError(t, json.Unmarshal(model.SelectedData, &roundTrip))
	assert.Equal(t, "g1", roundTrip.Release.GUID)
	assert.Equal(t, 3, roundTrip.Coverage)
}

func TestDecisionRepository_Save_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	d := decision.New(uuid.New(), "main", "Hijack", 122, 2)
	d.Outcome = decision.OutcomeSkip
	d.Reason = decision.ReasonSkipNoMissing
	err = repo.Save(context.Background(), d)
	require.Error(t, err)
}
