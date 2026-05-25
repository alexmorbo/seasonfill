package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
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
	d.DryRunWouldGrab = true
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
	// Regression guard for deferred-item #7: the DB column stays
	// `would_grab` even though the Go field was renamed.
	assert.True(t, model.DryRunWouldGrab, "column would_grab must round-trip into the renamed Go field")
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

// E-1: GetByID

func TestDecisionRepository_GetByID_Found(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)
	ctx := context.Background()

	d := decision.New(uuid.New(), "main", "Hijack", 122, 2)
	d.Outcome = decision.OutcomeSkip
	d.Reason = decision.ReasonSkipNoMissing
	d.MissingCount = 0
	d.ExistingCount = 5
	d.CreatedAt = time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.Save(ctx, d))

	got, err := repo.GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, d.ID, got.ID)
	assert.Equal(t, d.InstanceName, got.InstanceName)
	assert.Equal(t, d.Outcome, got.Outcome)
	assert.Equal(t, d.Reason, got.Reason)
	assert.Equal(t, d.MissingCount, got.MissingCount)
	assert.Equal(t, d.ExistingCount, got.ExistingCount)
}

func TestDecisionRepository_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)

	_, err := repo.GetByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestDecisionRepository_GetByID_MalformedRow(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)
	ctx := context.Background()

	// Insert a valid decision first so we have a real row to inspect.
	d := decision.New(uuid.New(), "main", "Hijack", 122, 2)
	d.Outcome = decision.OutcomeSkip
	d.Reason = decision.ReasonSkipNoMissing
	require.NoError(t, repo.Save(ctx, d))

	// Overwrite the filtered_out JSON column with invalid JSON via raw SQL.
	// toDecision attempts json.Unmarshal on this field and must return an error.
	res := db.Exec("UPDATE decisions SET filtered_out = ? WHERE id = ?", []byte("not-valid-json"), d.ID.String())
	require.NoError(t, res.Error)

	_, err := repo.GetByID(ctx, d.ID)
	require.Error(t, err, "GetByID must propagate toDecision unmarshal error")
}

// E-2: UpdateSupersededBy

func TestDecisionRepository_UpdateSupersededBy_Sets(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)
	ctx := context.Background()

	dA := decision.New(uuid.New(), "main", "Hijack", 122, 2)
	dA.Outcome = decision.OutcomeSkip
	dA.Reason = decision.ReasonSkipNoMissing
	require.NoError(t, repo.Save(ctx, dA))

	dB := decision.New(uuid.New(), "main", "Hijack", 122, 2)
	dB.Outcome = decision.OutcomeGrab
	dB.Reason = decision.ReasonGrabSelectedDryRun
	require.NoError(t, repo.Save(ctx, dB))

	require.NoError(t, repo.UpdateSupersededBy(ctx, dA.ID, dB.ID))

	got, err := repo.GetByID(ctx, dA.ID)
	require.NoError(t, err)
	require.NotNil(t, got.SupersededByID)
	assert.Equal(t, dB.ID, *got.SupersededByID)
}

func TestDecisionRepository_UpdateSupersededBy_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)

	err := repo.UpdateSupersededBy(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}
