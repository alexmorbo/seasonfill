package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestDecisionRepository_Save_NoSelected(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
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
		})
	}
}

func TestDecisionRepository_Save_WithSelected(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
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
		})
	}
}

func TestDecisionRepository_Save_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDecisionRepository(db)

			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())

			d := decision.New(uuid.New(), "main", "Hijack", 122, 2)
			d.Outcome = decision.OutcomeSkip
			d.Reason = decision.ReasonSkipNoMissing
			err = repo.Save(context.Background(), d)
			require.Error(t, err)
		})
	}
}

// TestDecisionRepository_Save_NilScanRunID_PersistsAsNULL — Story
// 121b §B: a decision.Decision with ScanRunID = uuid.Nil must
// round-trip as NULL through the repo (not the all-zero UUID string).
// The frontend guards on `d.scan_run_id && <Link>` and the all-zero
// UUID is truthy, so persisting it as text would re-introduce the
// dead-link bug.
func TestDecisionRepository_Save_NilScanRunID_PersistsAsNULL(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDecisionRepository(db)
			ctx := context.Background()

			d := decision.Decision{
				ID:           uuid.New(),
				ScanRunID:    uuid.Nil,
				InstanceName: "homelab",
				SeriesID:     42,
				SeasonNumber: 1,
				Outcome:      decision.OutcomeGrab,
				Reason:       decision.ReasonGrabSelected,
				CreatedAt:    time.Now().UTC(),
			}
			require.NoError(t, repo.Save(ctx, d))

			var raw sql.NullString
			require.NoError(t,
				db.Raw("SELECT scan_run_id FROM decisions WHERE id = ?", d.ID.String()).
					Scan(&raw).Error)
			assert.False(t, raw.Valid,
				"scan_run_id column must be SQL NULL, got %q", raw.String)

			// Round-trip — GetByID returns uuid.Nil, not the all-zero string.
			got, err := repo.GetByID(ctx, d.ID)
			require.NoError(t, err)
			assert.Equal(t, uuid.Nil, got.ScanRunID)
		})
	}
}

// E-1: GetByID

func TestDecisionRepository_GetByID_Found(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
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
		})
	}
}

func TestDecisionRepository_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDecisionRepository(db)

			missing := uuid.New()
			_, err := repo.GetByID(context.Background(), missing)
			require.Error(t, err)

			var typedErr *sharedErrors.DecisionNotFoundError
			require.True(t, errors.As(err, &typedErr),
				"GetByID NotFound must expose typed DecisionNotFoundError via errors.As")
			assert.Equal(t, missing, typedErr.ID)
		})
	}
}

func TestDecisionRepository_GetByID_MalformedRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDecisionRepository(db)
			ctx := context.Background()

			// Insert a valid decision first so we have a real row to inspect.
			d := decision.New(uuid.New(), "main", "Hijack", 122, 2)
			d.Outcome = decision.OutcomeSkip
			d.Reason = decision.ReasonSkipNoMissing
			require.NoError(t, repo.Save(ctx, d))

			// Overwrite the filtered_out JSON column with a payload that is
			// valid JSON syntax but cannot unmarshal into the expected
			// []decision.FilteredCandidate slice (a bare JSON string vs an
			// array). Raw invalid bytes work on SQLite (text column) but
			// Postgres' jsonb validation rejects them at write time — using
			// a structurally-wrong-but-valid-JSON payload exercises the
			// toDecision parse-failure path on BOTH backends.
			res := db.Exec("UPDATE decisions SET filtered_out = ? WHERE id = ?", []byte(`"not-an-array"`), d.ID.String())
			require.NoError(t, res.Error)

			_, err := repo.GetByID(ctx, d.ID)
			require.Error(t, err, "GetByID must propagate toDecision unmarshal error")
		})
	}
}

// E-2: UpdateSupersededBy

func TestDecisionRepository_UpdateSupersededBy_Sets(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
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
		})
	}
}

func TestDecisionRepository_UpdateSupersededBy_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDecisionRepository(db)

			err := repo.UpdateSupersededBy(context.Background(), uuid.New(), uuid.New())
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}
