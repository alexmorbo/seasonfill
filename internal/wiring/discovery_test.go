package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// stubCacheReader satisfies catalogLibraryInstancesReader for the
// adapter tests.
type stubCacheReader struct {
	out map[shareddomain.SeriesID][]shareddomain.InstanceName
	err error
}

func (s stubCacheReader) GetInstancesBySeriesIDs(_ context.Context, _ []shareddomain.SeriesID) (map[shareddomain.SeriesID][]shareddomain.InstanceName, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.out, nil
}

func TestLibraryInstancesAdapter_HappyPath_UnwrapsTypedSlice(t *testing.T) {
	t.Parallel()
	a := &libraryInstancesAdapter{
		cache: stubCacheReader{
			out: map[shareddomain.SeriesID][]shareddomain.InstanceName{
				1: {"alpha", "beta"},
				2: {"gamma"},
			},
		},
	}
	got, err := a.ListByCanonicalSeriesIDs(context.Background(),
		[]shareddomain.SeriesID{1, 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta"}, got[1])
	assert.Equal(t, []string{"gamma"}, got[2])
}

func TestLibraryInstancesAdapter_EmptyInput_NoCall(t *testing.T) {
	t.Parallel()
	a := &libraryInstancesAdapter{cache: stubCacheReader{
		err: errors.New("MUST NOT be called"),
	}}
	got, err := a.ListByCanonicalSeriesIDs(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got)
}

func TestLibraryInstancesAdapter_PropagatesError(t *testing.T) {
	t.Parallel()
	want := errors.New("db down")
	a := &libraryInstancesAdapter{cache: stubCacheReader{err: want}}
	_, err := a.ListByCanonicalSeriesIDs(context.Background(),
		[]shareddomain.SeriesID{1})
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
}
