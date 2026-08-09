package collections

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// fakeRepo returns canned results keyed by the first tmdb id in the set, so a
// test can control per-collection owned counts without a DB.
type fakeRepo struct {
	instances []string
	byFirstID map[int64]ports.CollectionResult
	err       error
}

func (f fakeRepo) DistinctInstances(context.Context) ([]string, error) {
	return f.instances, f.err
}

func (f fakeRepo) Collection(_ context.Context, _ string, ids []int64, _ int) (ports.CollectionResult, error) {
	if f.err != nil {
		return ports.CollectionResult{}, f.err
	}
	if len(ids) == 0 {
		return ports.CollectionResult{}, nil
	}
	return f.byFirstID[ids[0]], nil
}

func TestUseCase_Build_HidesEmptyAndSortsDesc(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

	// books (id 818) → 40 ; comics (id 9715) → 7 ; everything else → 0 (hidden).
	repo := fakeRepo{
		instances: []string{"homelab"},
		byFirstID: map[int64]ports.CollectionResult{
			818:  {OwnedCount: 40, Series: []ports.CollectionSeriesRow{{SeriesID: 1, Title: "Z"}}},
			9715: {OwnedCount: 7, Series: []ports.CollectionSeriesRow{{SeriesID: 2, Title: "A"}}},
		},
	}
	uc := NewUseCase(repo).WithClock(func() time.Time { return fixed })

	rep, err := uc.Build(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, fixed, rep.GeneratedAt)
	require.Len(t, rep.Instances, 1)

	cols := rep.Instances[0].Collections
	// only the two non-empty collections survive, books (40) before comics (7)
	require.Len(t, cols, 2)
	assert.Equal(t, "books", cols[0].Slug)
	assert.Equal(t, 40, cols[0].OwnedCount)
	assert.Equal(t, "comics", cols[1].Slug)
	assert.Equal(t, 7, cols[1].OwnedCount)
	// mcu (id 180547 → 0) is hidden
	for _, c := range cols {
		assert.NotEqual(t, "mcu", c.Slug)
	}
}

func TestUseCase_Build_InstanceFilterSkipsDistinct(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{
		instances: []string{"should", "not", "be", "used"},
		byFirstID: map[int64]ports.CollectionResult{818: {OwnedCount: 1}},
	}
	uc := NewUseCase(repo)
	rep, err := uc.Build(context.Background(), "homelab")
	require.NoError(t, err)
	require.Len(t, rep.Instances, 1)
	assert.Equal(t, "homelab", rep.Instances[0].InstanceName)
}

func TestUseCase_Build_IsFranchiseFlag(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{
		instances: []string{"homelab"},
		byFirstID: map[int64]ports.CollectionResult{180547: {OwnedCount: 3}},
	}
	rep, err := NewUseCase(repo).Build(context.Background(), "homelab")
	require.NoError(t, err)
	require.Len(t, rep.Instances, 1)
	require.Len(t, rep.Instances[0].Collections, 1)
	c := rep.Instances[0].Collections[0]
	assert.Equal(t, "mcu", c.Slug)
	assert.True(t, c.IsFranchise)
}
