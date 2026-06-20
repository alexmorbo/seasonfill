package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestDecisionRepository_SaveAndLoad_SeasonStatsRoundTrip wires the
// real repo against an in-memory SQLite DB (created by the shared
// helper in this package) and asserts the 4 new fields round-trip
// through Save → GetByID.
func TestDecisionRepository_SaveAndLoad_SeasonStatsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDecisionRepository(db)

			scanID := uuid.New()
			d := decision.Decision{
				ID:               uuid.New(),
				ScanRunID:        scanID,
				InstanceName:     "homelab",
				SeriesID:         101,
				SeriesTitle:      "Severance",
				SeasonNumber:     2,
				Outcome:          decision.OutcomeSkip,
				Reason:           decision.ReasonSkipNoMissing,
				MissingCount:     0,
				ExistingCount:    10,
				TotalEpisodes:    10,
				AiredEpisodes:    10,
				ExistingEpisodes: 10,
				GrabbedEpisodes:  4,
				CreatedAt:        time.Now().UTC().Truncate(time.Second),
			}

			require.NoError(t, repo.Save(context.Background(), d))
			got, err := repo.GetByID(context.Background(), d.ID)
			require.NoError(t, err)
			assert.Equal(t, 10, got.TotalEpisodes)
			assert.Equal(t, 10, got.AiredEpisodes)
			assert.Equal(t, 10, got.ExistingEpisodes)
			assert.Equal(t, 4, got.GrabbedEpisodes)
		})
	}
}

// TestDecisionRepository_List_ReturnsSeasonStats asserts the new fields
// also flow through the List path that powers GET /api/v1/decisions.
func TestDecisionRepository_List_ReturnsSeasonStats(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDecisionRepository(db)

			d := decision.Decision{
				ID: uuid.New(), ScanRunID: uuid.New(),
				InstanceName: "homelab", SeriesID: 5, SeasonNumber: 1,
				Outcome: decision.OutcomeGrab, Reason: decision.ReasonGrabSelectedDryRun,
				TotalEpisodes: 8, AiredEpisodes: 6, ExistingEpisodes: 2, GrabbedEpisodes: 1,
				CreatedAt: time.Now().UTC().Truncate(time.Second),
			}
			require.NoError(t, repo.Save(context.Background(), d))

			out, _, err := repo.List(context.Background(), ports.DecisionFilter{}, ports.Pagination{Limit: 10})
			require.NoError(t, err)
			require.Len(t, out, 1)
			assert.Equal(t, 8, out[0].TotalEpisodes)
			assert.Equal(t, 6, out[0].AiredEpisodes)
			assert.Equal(t, 2, out[0].ExistingEpisodes)
			assert.Equal(t, 1, out[0].GrabbedEpisodes)
		})
	}
}
