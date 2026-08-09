package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeSeriesReader struct {
	canon series.Canon
	err   error
	calls int
}

func (f *fakeSeriesReader) Get(_ context.Context, _ domain.SeriesID) (series.Canon, error) {
	f.calls++
	return f.canon, f.err
}

type fakeStore struct {
	followed  map[domain.SeriesID]bool
	followErr error
	unfollowN int
	followN   int
	listItems []FollowedItem
	listErr   error
	lastLang  string
}

func newFakeStore() *fakeStore { return &fakeStore{followed: map[domain.SeriesID]bool{}} }

func (f *fakeStore) Follow(_ context.Context, id domain.SeriesID) error {
	f.followN++
	if f.followErr != nil {
		return f.followErr
	}
	f.followed[id] = true
	return nil
}

func (f *fakeStore) Unfollow(_ context.Context, id domain.SeriesID) error {
	f.unfollowN++
	delete(f.followed, id)
	return nil
}

func (f *fakeStore) ListFollowed(_ context.Context, lang string) ([]FollowedItem, error) {
	f.lastLang = lang
	return f.listItems, f.listErr
}

type enricherCall struct {
	id  domain.SeriesID
	hyd series.Hydration
}

type recordingEnricher struct {
	calls []enricherCall
}

func (r *recordingEnricher) EnqueueIfStale(id domain.SeriesID, hyd series.Hydration) {
	r.calls = append(r.calls, enricherCall{id: id, hyd: hyd})
}

func TestFollowUseCase_Follow_PromoteEnrolls(t *testing.T) {
	t.Parallel()
	reader := &fakeSeriesReader{canon: series.Canon{Hydration: series.HydrationStub}}
	store := newFakeStore()
	enr := &recordingEnricher{}
	uc, err := NewFollowUseCase(reader, store, enr, nil)
	require.NoError(t, err)

	require.NoError(t, uc.Follow(context.Background(), domain.SeriesID(140)))
	assert.Equal(t, 1, store.followN, "store.Follow called once")
	assert.True(t, store.followed[140])
	require.Len(t, enr.calls, 1, "enricher enqueued once")
	assert.Equal(t, domain.SeriesID(140), enr.calls[0].id)
	assert.Equal(t, series.HydrationStub, enr.calls[0].hyd)
}

func TestFollowUseCase_Follow_NotFound(t *testing.T) {
	t.Parallel()
	reader := &fakeSeriesReader{err: ports.ErrNotFound}
	store := newFakeStore()
	enr := &recordingEnricher{}
	uc, err := NewFollowUseCase(reader, store, enr, nil)
	require.NoError(t, err)

	err = uc.Follow(context.Background(), domain.SeriesID(999))
	require.ErrorIs(t, err, ErrSeriesNotFound)
	assert.Equal(t, 0, store.followN, "store.Follow NOT called on missing canon")
	assert.Empty(t, enr.calls, "enricher NOT called on missing canon")
}

func TestFollowUseCase_Follow_InvalidID(t *testing.T) {
	t.Parallel()
	reader := &fakeSeriesReader{canon: series.Canon{Hydration: series.HydrationStub}}
	store := newFakeStore()
	enr := &recordingEnricher{}
	uc, err := NewFollowUseCase(reader, store, enr, nil)
	require.NoError(t, err)

	err = uc.Follow(context.Background(), domain.SeriesID(0))
	require.ErrorIs(t, err, ErrInvalidSeriesID)
	assert.Equal(t, 0, reader.calls)
	assert.Equal(t, 0, store.followN)
	assert.Empty(t, enr.calls)
}

func TestFollowUseCase_Follow_NilEnricher(t *testing.T) {
	t.Parallel()
	reader := &fakeSeriesReader{canon: series.Canon{Hydration: series.HydrationFull}}
	store := newFakeStore()
	uc, err := NewFollowUseCase(reader, store, nil, nil)
	require.NoError(t, err)

	require.NoError(t, uc.Follow(context.Background(), domain.SeriesID(7)))
	assert.Equal(t, 1, store.followN, "store.Follow still called with nil enricher")
}

func TestFollowUseCase_Unfollow_Idempotent(t *testing.T) {
	t.Parallel()
	reader := &fakeSeriesReader{canon: series.Canon{Hydration: series.HydrationStub}}
	store := newFakeStore()
	uc, err := NewFollowUseCase(reader, store, nil, nil)
	require.NoError(t, err)

	require.NoError(t, uc.Unfollow(context.Background(), domain.SeriesID(5)))
	require.NoError(t, uc.Unfollow(context.Background(), domain.SeriesID(5)))
	assert.Equal(t, 2, store.unfollowN)
}

func TestFollowUseCase_Unfollow_InvalidID(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	uc, err := NewFollowUseCase(&fakeSeriesReader{}, store, nil, nil)
	require.NoError(t, err)
	require.ErrorIs(t, uc.Unfollow(context.Background(), domain.SeriesID(-1)), ErrInvalidSeriesID)
	assert.Equal(t, 0, store.unfollowN)
}

func TestFollowUseCase_ListFollowed_PassThrough(t *testing.T) {
	t.Parallel()
	want := []FollowedItem{{SeriesID: 1, Title: "A"}, {SeriesID: 2, Title: "B"}}
	store := newFakeStore()
	store.listItems = want
	uc, err := NewFollowUseCase(&fakeSeriesReader{}, store, nil, nil)
	require.NoError(t, err)

	got, err := uc.ListFollowed(context.Background(), "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, "ru-RU", store.lastLang)
}

func TestFollowUseCase_ListFollowed_Error(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.listErr = errors.New("boom")
	uc, err := NewFollowUseCase(&fakeSeriesReader{}, store, nil, nil)
	require.NoError(t, err)
	_, err = uc.ListFollowed(context.Background(), "en-US")
	require.Error(t, err)
}

func TestNewFollowUseCase_NilDeps(t *testing.T) {
	t.Parallel()
	_, err := NewFollowUseCase(nil, newFakeStore(), nil, nil)
	require.Error(t, err)
	_, err = NewFollowUseCase(&fakeSeriesReader{}, nil, nil, nil)
	require.Error(t, err)
}
