package gc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeOrphanRepo struct {
	candidates    []domain.SeriesID
	listCutoff    time.Time
	listErr       error
	dropFailures  map[domain.SeriesID]bool
	dropCallCount int
}

func (f *fakeOrphanRepo) ListOrphanCandidates(_ context.Context, cutoff time.Time, _ int) ([]domain.SeriesID, error) {
	f.listCutoff = cutoff
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.candidates, nil
}

func (f *fakeOrphanRepo) DropSeriesCascade(_ context.Context, id domain.SeriesID) error {
	f.dropCallCount++
	if f.dropFailures != nil && f.dropFailures[id] {
		return errors.New("drop failed")
	}
	return nil
}

func TestOrphanSeries_HappyPath(t *testing.T) {
	t.Parallel()
	repo := &fakeOrphanRepo{candidates: []domain.SeriesID{1, 2, 3}}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	build := OrphanSeriesDeps{
		Repo:  repo,
		Clock: func() time.Time { return now },
	}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, res.Candidates)
	assert.Equal(t, 3, res.Deleted)
	expectedCutoff := now.Add(-90 * 24 * time.Hour)
	assert.True(t, repo.listCutoff.Equal(expectedCutoff))
}

func TestOrphanSeries_CustomGrace(t *testing.T) {
	t.Parallel()
	repo := &fakeOrphanRepo{candidates: []domain.SeriesID{1}}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	build := OrphanSeriesDeps{
		Repo:          repo,
		Clock:         func() time.Time { return now },
		GraceDuration: 24 * time.Hour,
	}.Build()
	_, err := build(context.Background())
	require.NoError(t, err)
	assert.True(t, repo.listCutoff.Equal(now.Add(-24*time.Hour)))
}

func TestOrphanSeries_PerRowFailure_Continues(t *testing.T) {
	t.Parallel()
	repo := &fakeOrphanRepo{
		candidates:   []domain.SeriesID{1, 2, 3},
		dropFailures: map[domain.SeriesID]bool{2: true},
	}
	build := OrphanSeriesDeps{Repo: repo}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, res.Candidates)
	assert.Equal(t, 2, res.Deleted)
	assert.Equal(t, 3, repo.dropCallCount)
}

func TestOrphanSeries_ListError(t *testing.T) {
	t.Parallel()
	repo := &fakeOrphanRepo{listErr: errors.New("db down")}
	build := OrphanSeriesDeps{Repo: repo}.Build()
	_, err := build(context.Background())
	require.Error(t, err)
}
