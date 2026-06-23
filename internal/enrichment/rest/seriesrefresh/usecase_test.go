package seriesrefresh

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichment "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type refreshFakeCache struct {
	entry series.CacheEntry
	err   error
}

func (f *refreshFakeCache) Get(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) (series.CacheEntry, error) {
	return f.entry, f.err
}
func (f *refreshFakeCache) Upsert(_ context.Context, _ series.CacheEntry) error { return nil }
func (f *refreshFakeCache) SoftDelete(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) error {
	return nil
}
func (f *refreshFakeCache) ListActiveByInstance(_ context.Context, _ domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *refreshFakeCache) ListByFilter(_ context.Context, _ domain.InstanceName, _ ports.SeriesCacheFilter, _ ports.SeriesCacheSort, _ ports.Pagination) ([]series.CacheEntry, int, bool, *ports.Cursor, error) {
	return nil, 0, false, nil, nil
}
func (f *refreshFakeCache) FetchLastGrabInfo(_ context.Context, _ domain.InstanceName, _ []domain.SonarrSeriesID) (map[domain.SonarrSeriesID]ports.LastGrabInfo, error) {
	return make(map[domain.SonarrSeriesID]ports.LastGrabInfo), nil
}
func (f *refreshFakeCache) ListDistinctNetworks(_ context.Context, _ domain.InstanceName) ([]string, error) {
	return nil, nil
}
func (f *refreshFakeCache) GetInstancesBySeriesID(_ context.Context, _ domain.SeriesID) ([]domain.InstanceName, error) {
	return nil, nil
}
func (f *refreshFakeCache) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	return nil, nil
}

type refreshFakeSeries struct {
	canon CanonView
	err   error
}

func (f *refreshFakeSeries) Get(_ context.Context, _ domain.SeriesID) (CanonView, error) {
	return f.canon, f.err
}

type refreshFakeCast struct {
	ids []int64
	err error
}

func (f *refreshFakeCast) TopCastPersonIDs(_ context.Context, _ domain.SeriesID, _ int) ([]int64, error) {
	return f.ids, f.err
}

type refreshFakeDispatcher struct {
	enqueued []enrichEntry
}

type enrichEntry struct {
	kind enrichment.EntityKind
	id   int64
	prio enrichment.Priority
}

func (d *refreshFakeDispatcher) Enqueue(k enrichment.EntityKind, id int64, p enrichment.Priority) {
	d.enqueued = append(d.enqueued, enrichEntry{k, id, p})
}
func (d *refreshFakeDispatcher) Close() {}

func ptrIMDBID(v string) *domain.IMDBID { id := domain.IMDBID(v); return &id }
func ptrSeriesID(v int64) *domain.SeriesID {
	id := domain.SeriesID(v)
	return &id
}

func TestRefresh_HappyPath_SeriesPersonsOMDb(t *testing.T) {
	t.Parallel()
	cache := &refreshFakeCache{entry: series.CacheEntry{SeriesID: ptrSeriesID(99)}}
	canon := &refreshFakeSeries{canon: CanonView{ID: 99, IMDBID: ptrIMDBID("tt123")}}
	cast := &refreshFakeCast{ids: []int64{1, 2, 3}}
	disp := &refreshFakeDispatcher{}

	uc, err := New(Deps{SeriesCache: cache, Series: canon, SeriesPeople: cast, Dispatcher: disp})
	require.NoError(t, err)

	res, err := uc.Refresh(context.Background(), "alpha", 7)
	require.NoError(t, err)
	assert.Equal(t, domain.SeriesID(99), res.SeriesID)
	assert.True(t, res.SeriesQueued)
	assert.Equal(t, 3, res.Persons)
	assert.True(t, res.OMDbQueued)
	require.Len(t, disp.enqueued, 5)
	assert.Equal(t, enrichment.EntitySeries, disp.enqueued[0].kind)
	assert.Equal(t, enrichment.PriorityHot, disp.enqueued[0].prio)
}

func TestRefresh_NoSeriesPeople_SkipsCastBranch(t *testing.T) {
	t.Parallel()
	cache := &refreshFakeCache{entry: series.CacheEntry{SeriesID: ptrSeriesID(50)}}
	canon := &refreshFakeSeries{canon: CanonView{ID: 50}}
	disp := &refreshFakeDispatcher{}

	uc, err := New(Deps{SeriesCache: cache, Series: canon, Dispatcher: disp})
	require.NoError(t, err)
	res, err := uc.Refresh(context.Background(), "alpha", 5)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Persons)
	assert.False(t, res.OMDbQueued)
	assert.Len(t, disp.enqueued, 1)
}

func TestRefresh_NoIMDB_SkipsOMDb(t *testing.T) {
	t.Parallel()
	cache := &refreshFakeCache{entry: series.CacheEntry{SeriesID: ptrSeriesID(50)}}
	canon := &refreshFakeSeries{canon: CanonView{ID: 50, IMDBID: ptrIMDBID("")}}
	cast := &refreshFakeCast{ids: []int64{1}}
	disp := &refreshFakeDispatcher{}

	uc, err := New(Deps{SeriesCache: cache, Series: canon, SeriesPeople: cast, Dispatcher: disp})
	require.NoError(t, err)
	res, err := uc.Refresh(context.Background(), "alpha", 5)
	require.NoError(t, err)
	assert.False(t, res.OMDbQueued)
}

func TestRefresh_CacheMiss_NotFound(t *testing.T) {
	t.Parallel()
	cache := &refreshFakeCache{err: ports.ErrNotFound}
	canon := &refreshFakeSeries{}
	disp := &refreshFakeDispatcher{}

	uc, err := New(Deps{SeriesCache: cache, Series: canon, Dispatcher: disp})
	require.NoError(t, err)
	_, err = uc.Refresh(context.Background(), "alpha", 5)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestRefresh_CacheNoCanonID_NotFound(t *testing.T) {
	t.Parallel()
	cache := &refreshFakeCache{entry: series.CacheEntry{}}
	canon := &refreshFakeSeries{}
	disp := &refreshFakeDispatcher{}

	uc, err := New(Deps{SeriesCache: cache, Series: canon, Dispatcher: disp})
	require.NoError(t, err)
	_, err = uc.Refresh(context.Background(), "alpha", 5)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestRefresh_TopCastFails_LogsAndContinues(t *testing.T) {
	t.Parallel()
	cache := &refreshFakeCache{entry: series.CacheEntry{SeriesID: ptrSeriesID(7)}}
	canon := &refreshFakeSeries{canon: CanonView{ID: 7}}
	cast := &refreshFakeCast{err: errors.New("db down")}
	disp := &refreshFakeDispatcher{}

	uc, err := New(Deps{SeriesCache: cache, Series: canon, SeriesPeople: cast, Dispatcher: disp})
	require.NoError(t, err)
	res, err := uc.Refresh(context.Background(), "alpha", 5)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Persons)
	assert.True(t, res.SeriesQueued)
}

func TestNew_RequiredFields(t *testing.T) {
	t.Parallel()
	_, err := New(Deps{})
	require.Error(t, err)
	_, err = New(Deps{SeriesCache: &refreshFakeCache{}})
	require.Error(t, err)
	_, err = New(Deps{SeriesCache: &refreshFakeCache{}, Series: &refreshFakeSeries{}})
	require.Error(t, err)
}
