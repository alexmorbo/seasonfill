package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
)

func seedDecision(t *testing.T, db *gorm.DB, scanRunID uuid.UUID, instance string, seriesID, season int, outcome decision.Outcome, createdAt time.Time) decision.Decision {
	t.Helper()
	d := decision.New(scanRunID, instance, "Hijack", seriesID, season)
	d.Outcome = outcome
	d.Reason = decision.ReasonSkipNoMissing
	d.CreatedAt = createdAt
	require.NoError(t, NewDecisionRepository(db).Save(context.Background(), d))
	return d
}

func TestDecisionRepository_List_Empty(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	got, next, err := NewDecisionRepository(db).List(context.Background(), ports.DecisionFilter{}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Nil(t, next)
}

func TestDecisionRepository_List_FirstAndSecondPage(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	scanRun := uuid.New()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		seedDecision(t, db, scanRun, "main", 100+i, 1, decision.OutcomeSkip, base.Add(time.Duration(i)*time.Second))
	}

	repo := NewDecisionRepository(db)
	ctx := context.Background()
	first, next, err := repo.List(ctx, ports.DecisionFilter{}, ports.Pagination{Limit: 3})
	require.NoError(t, err)
	require.Len(t, first, 3)
	require.NotNil(t, next)

	second, next2, err := repo.List(ctx, ports.DecisionFilter{}, ports.Pagination{Limit: 3, Cursor: next})
	require.NoError(t, err)
	require.Len(t, second, 2)
	assert.Nil(t, next2)

	seen := map[string]bool{}
	for _, d := range append(first, second...) {
		assert.False(t, seen[d.ID.String()])
		seen[d.ID.String()] = true
	}
	assert.Len(t, seen, 5)
}

func TestDecisionRepository_List_InstanceAndSeriesFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	scanRun := uuid.New()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDecision(t, db, scanRun, "main", 100, 1, decision.OutcomeSkip, base)
	seedDecision(t, db, scanRun, "main", 100, 2, decision.OutcomeSkip, base.Add(time.Second))
	seedDecision(t, db, scanRun, "main", 200, 1, decision.OutcomeSkip, base.Add(2*time.Second))
	seedDecision(t, db, scanRun, "secondary", 100, 1, decision.OutcomeSkip, base.Add(3*time.Second))

	instance := "main"
	seriesID := 100
	got, _, err := NewDecisionRepository(db).List(context.Background(),
		ports.DecisionFilter{Instance: &instance, SeriesID: &seriesID},
		ports.Pagination{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, d := range got {
		assert.Equal(t, "main", d.InstanceName)
		assert.Equal(t, 100, d.SeriesID)
	}
}

func TestDecisionRepository_List_ScanRunIDFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	scanA := uuid.New()
	scanB := uuid.New()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDecision(t, db, scanA, "main", 100, 1, decision.OutcomeSkip, base)
	seedDecision(t, db, scanA, "main", 101, 1, decision.OutcomeSkip, base.Add(time.Second))
	seedDecision(t, db, scanB, "main", 200, 1, decision.OutcomeSkip, base.Add(2*time.Second))

	got, _, err := NewDecisionRepository(db).List(context.Background(),
		ports.DecisionFilter{ScanRunID: &scanA}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, d := range got {
		assert.Equal(t, scanA, d.ScanRunID)
	}
}

func TestDecisionRepository_List_OutcomeFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	scanRun := uuid.New()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDecision(t, db, scanRun, "main", 100, 1, decision.OutcomeGrab, base)
	seedDecision(t, db, scanRun, "main", 101, 1, decision.OutcomeSkip, base.Add(time.Second))
	seedDecision(t, db, scanRun, "main", 102, 1, decision.OutcomeSkip, base.Add(2*time.Second))

	want := "grab"
	got, _, err := NewDecisionRepository(db).List(context.Background(),
		ports.DecisionFilter{Decision: &want}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, decision.OutcomeGrab, got[0].Outcome)
}

func TestDecisionRepository_List_TimeRange(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	scanRun := uuid.New()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		seedDecision(t, db, scanRun, "main", 100+i, 1, decision.OutcomeSkip, base.Add(time.Duration(i)*time.Second))
	}

	from := base.Add(2 * time.Second)
	to := base.Add(5 * time.Second)
	got, _, err := NewDecisionRepository(db).List(context.Background(),
		ports.DecisionFilter{From: &from, To: &to}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestDecisionRepository_List_LimitDefensive(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewDecisionRepository(db)
	for _, lim := range []int{0, -5, ports.MaxListLimit + 1} {
		_, _, err := repo.List(context.Background(), ports.DecisionFilter{}, ports.Pagination{Limit: lim})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ports.ErrInvalidLimit))
	}
}
