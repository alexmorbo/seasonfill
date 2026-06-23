package scan

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type cascadeFakeCache struct {
	softDeleteCalls atomic.Int32
	softDeleteErr   error
}

func (f *cascadeFakeCache) Get(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) (series.CacheEntry, error) {
	return series.CacheEntry{}, ports.ErrNotFound
}
func (f *cascadeFakeCache) Upsert(_ context.Context, _ series.CacheEntry) error { return nil }
func (f *cascadeFakeCache) SoftDelete(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) error {
	f.softDeleteCalls.Add(1)
	return f.softDeleteErr
}
func (f *cascadeFakeCache) ListActiveByInstance(_ context.Context, _ domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *cascadeFakeCache) ListByFilter(_ context.Context, _ domain.InstanceName, _ ports.SeriesCacheFilter, _ ports.SeriesCacheSort, _ ports.Pagination) ([]series.CacheEntry, int, bool, *ports.Cursor, error) {
	return nil, 0, false, nil, nil
}
func (f *cascadeFakeCache) FetchLastGrabInfo(_ context.Context, _ domain.InstanceName, _ []domain.SonarrSeriesID) (map[domain.SonarrSeriesID]ports.LastGrabInfo, error) {
	return make(map[domain.SonarrSeriesID]ports.LastGrabInfo), nil
}
func (f *cascadeFakeCache) ListDistinctNetworks(_ context.Context, _ domain.InstanceName) ([]string, error) {
	return nil, nil
}
func (f *cascadeFakeCache) GetInstancesBySeriesID(_ context.Context, _ domain.SeriesID) ([]domain.InstanceName, error) {
	return nil, nil
}

type cascadeFakeEpisodes struct {
	calls        atomic.Int32
	rowsToReturn int
	errToReturn  error
}

func (f *cascadeFakeEpisodes) SoftDeleteBySeries(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) (int, error) {
	f.calls.Add(1)
	if f.errToReturn != nil {
		return 0, f.errToReturn
	}
	return f.rowsToReturn, nil
}

type cascadeFakeTx struct {
	calls atomic.Int32
}

func (t *cascadeFakeTx) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	t.calls.Add(1)
	return fn(ctx)
}

func TestCascadeSeriesDelete_BothSides_OK(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{}
	eps := &cascadeFakeEpisodes{rowsToReturn: 17}
	tx := &cascadeFakeTx{}

	cacheDeleted, rows, seasonRows, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache:   cache,
		EpisodeStates: eps,
		Tx:            tx,
	}, "alpha", 42)
	require.NoError(t, err)
	assert.True(t, cacheDeleted)
	assert.Equal(t, 17, rows)
	assert.Equal(t, 0, seasonRows)
	assert.Equal(t, int32(1), cache.softDeleteCalls.Load())
	assert.Equal(t, int32(1), eps.calls.Load())
	assert.Equal(t, int32(1), tx.calls.Load())
}

func TestCascadeSeriesDelete_NilEpisodes_CacheOnly(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{}
	cacheDeleted, rows, seasonRows, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache: cache,
	}, "alpha", 42)
	require.NoError(t, err)
	assert.True(t, cacheDeleted)
	assert.Equal(t, 0, rows)
	assert.Equal(t, 0, seasonRows)
	assert.Equal(t, int32(1), cache.softDeleteCalls.Load())
}

func TestCascadeSeriesDelete_CacheError_ShortCircuits(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{softDeleteErr: errors.New("db down")}
	eps := &cascadeFakeEpisodes{rowsToReturn: 7}
	_, _, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache:   cache,
		EpisodeStates: eps,
	}, "alpha", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
	assert.Equal(t, int32(0), eps.calls.Load(), "episode side must NOT run after cache failure")
}

func TestCascadeSeriesDelete_Idempotent(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{}
	eps := &cascadeFakeEpisodes{}
	for range 3 {
		_, _, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
			SeriesCache:   cache,
			EpisodeStates: eps,
		}, "alpha", 42)
		require.NoError(t, err)
	}
	assert.Equal(t, int32(3), cache.softDeleteCalls.Load())
	assert.Equal(t, int32(3), eps.calls.Load())
}

func TestCascadeSeriesDelete_EmptyInstance_Errors(t *testing.T) {
	t.Parallel()
	_, _, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache: &cascadeFakeCache{},
	}, "", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance_name")
}

func TestCascadeSeriesDelete_ZeroSeriesID_Errors(t *testing.T) {
	t.Parallel()
	_, _, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache: &cascadeFakeCache{},
	}, "alpha", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sonarr_series_id")
}

func TestCascadeSeriesDelete_NoSeriesCache_Errors(t *testing.T) {
	t.Parallel()
	_, _, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{}, "alpha", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SeriesCache")
}

// TestCascadeSeriesDelete_SeasonStatsBranch — story 377. When a
// SeasonStats deleter is wired in, the cascade soft-deletes the
// season_stats rows alongside cache + episode_states.
func TestCascadeSeriesDelete_SeasonStatsBranch(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{}
	ep := &cascadeFakeEpisodes{rowsToReturn: 0}
	ss := &fakeSeasonStatsSoftDelete{n: 5}

	cacheDeleted, epRows, ssRows, err := CascadeSeriesDelete(
		context.Background(),
		CascadeDeleteDeps{SeriesCache: cache, EpisodeStates: ep, SeasonStats: ss},
		"homelab", 140,
	)
	require.NoError(t, err)
	assert.True(t, cacheDeleted)
	assert.Equal(t, 0, epRows)
	assert.Equal(t, 5, ssRows)
	assert.Equal(t, int32(1), ss.calls.Load(), "SoftDeleteBySeries must be invoked exactly once")
}

// TestCascadeSeriesDelete_NilSeasonStats_StillSoftDeletesCacheAndEpisodes —
// story 377 keeps the nil-tolerant contract: missing SeasonStats port
// must not break the cascade (back-compat for older test wirings).
func TestCascadeSeriesDelete_NilSeasonStats_StillSoftDeletesCacheAndEpisodes(t *testing.T) {
	t.Parallel()
	cache := &cascadeFakeCache{}
	ep := &cascadeFakeEpisodes{rowsToReturn: 3}

	cacheDeleted, epRows, ssRows, err := CascadeSeriesDelete(
		context.Background(),
		CascadeDeleteDeps{SeriesCache: cache, EpisodeStates: ep, SeasonStats: nil},
		"homelab", 140,
	)
	require.NoError(t, err)
	assert.True(t, cacheDeleted)
	assert.Equal(t, 3, epRows)
	assert.Equal(t, 0, ssRows)
}

type fakeSeasonStatsSoftDelete struct {
	n     int
	err   error
	calls atomic.Int32
}

func (f *fakeSeasonStatsSoftDelete) SoftDeleteBySeries(
	_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID,
) (int, error) {
	f.calls.Add(1)
	if f.err != nil {
		return 0, f.err
	}
	return f.n, nil
}
