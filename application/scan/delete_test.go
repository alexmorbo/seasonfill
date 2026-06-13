package scan

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
)

type cascadeFakeCache struct {
	softDeleteCalls int32
	softDeleteErr   error
}

func (f *cascadeFakeCache) Get(_ context.Context, _ string, _ int) (series.CacheEntry, error) {
	return series.CacheEntry{}, ports.ErrNotFound
}
func (f *cascadeFakeCache) Upsert(_ context.Context, _ series.CacheEntry) error { return nil }
func (f *cascadeFakeCache) SoftDelete(_ context.Context, _ string, _ int) error {
	atomic.AddInt32(&f.softDeleteCalls, 1)
	return f.softDeleteErr
}
func (f *cascadeFakeCache) ListActiveByInstance(_ context.Context, _ string) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *cascadeFakeCache) ListByFilter(_ context.Context, _ string, _ ports.SeriesCacheFilter, _ ports.SeriesCacheSort, _ ports.Pagination) ([]series.CacheEntry, int, bool, *ports.Cursor, error) {
	return nil, 0, false, nil, nil
}
func (f *cascadeFakeCache) FetchLastGrabInfo(_ context.Context, _ string, _ []int) (map[int]ports.LastGrabInfo, error) {
	return make(map[int]ports.LastGrabInfo), nil
}
func (f *cascadeFakeCache) ListDistinctNetworks(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

type cascadeFakeEpisodes struct {
	calls        int32
	rowsToReturn int
	errToReturn  error
}

func (f *cascadeFakeEpisodes) SoftDeleteBySeries(_ context.Context, _ string, _ int) (int, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.errToReturn != nil {
		return 0, f.errToReturn
	}
	return f.rowsToReturn, nil
}

type cascadeFakeTx struct {
	calls int32
}

func (t *cascadeFakeTx) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	atomic.AddInt32(&t.calls, 1)
	return fn(ctx)
}

func TestCascadeSeriesDelete_BothSides_OK(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{}
	eps := &cascadeFakeEpisodes{rowsToReturn: 17}
	tx := &cascadeFakeTx{}

	cacheDeleted, rows, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache:   cache,
		EpisodeStates: eps,
		Tx:            tx,
	}, "alpha", 42)
	require.NoError(t, err)
	assert.True(t, cacheDeleted)
	assert.Equal(t, 17, rows)
	assert.Equal(t, int32(1), atomic.LoadInt32(&cache.softDeleteCalls))
	assert.Equal(t, int32(1), atomic.LoadInt32(&eps.calls))
	assert.Equal(t, int32(1), atomic.LoadInt32(&tx.calls))
}

func TestCascadeSeriesDelete_NilEpisodes_CacheOnly(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{}
	cacheDeleted, rows, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache: cache,
	}, "alpha", 42)
	require.NoError(t, err)
	assert.True(t, cacheDeleted)
	assert.Equal(t, 0, rows)
	assert.Equal(t, int32(1), atomic.LoadInt32(&cache.softDeleteCalls))
}

func TestCascadeSeriesDelete_CacheError_ShortCircuits(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{softDeleteErr: errors.New("db down")}
	eps := &cascadeFakeEpisodes{rowsToReturn: 7}
	_, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache:   cache,
		EpisodeStates: eps,
	}, "alpha", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
	assert.Equal(t, int32(0), atomic.LoadInt32(&eps.calls), "episode side must NOT run after cache failure")
}

func TestCascadeSeriesDelete_Idempotent(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{}
	eps := &cascadeFakeEpisodes{}
	for i := 0; i < 3; i++ {
		_, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
			SeriesCache:   cache,
			EpisodeStates: eps,
		}, "alpha", 42)
		require.NoError(t, err)
	}
	assert.Equal(t, int32(3), atomic.LoadInt32(&cache.softDeleteCalls))
	assert.Equal(t, int32(3), atomic.LoadInt32(&eps.calls))
}

func TestCascadeSeriesDelete_EmptyInstance_Errors(t *testing.T) {
	t.Parallel()
	_, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache: &cascadeFakeCache{},
	}, "", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance_name")
}

func TestCascadeSeriesDelete_ZeroSeriesID_Errors(t *testing.T) {
	t.Parallel()
	_, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache: &cascadeFakeCache{},
	}, "alpha", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sonarr_series_id")
}

func TestCascadeSeriesDelete_NoSeriesCache_Errors(t *testing.T) {
	t.Parallel()
	_, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{}, "alpha", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SeriesCache")
}
