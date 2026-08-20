package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeMovieReader struct {
	canon movie.Canon
	err   error
	calls int
	last  domain.TMDBID
}

func (f *fakeMovieReader) GetByTMDBID(_ context.Context, id domain.TMDBID) (movie.Canon, error) {
	f.calls++
	f.last = id
	return f.canon, f.err
}

type fakeMovieStore struct {
	followed   map[domain.MovieID]bool
	followErr  error
	followN    int
	unfollowN  int
	listItems  []FollowedMovieItem
	listErr    error
	lastLang   string
	lastUserID int64
}

func newFakeMovieStore() *fakeMovieStore {
	return &fakeMovieStore{followed: map[domain.MovieID]bool{}}
}

func (f *fakeMovieStore) Follow(_ context.Context, uid int64, id domain.MovieID) error {
	f.followN++
	f.lastUserID = uid
	if f.followErr != nil {
		return f.followErr
	}
	f.followed[id] = true
	return nil
}

func (f *fakeMovieStore) Unfollow(_ context.Context, uid int64, id domain.MovieID) error {
	f.unfollowN++
	f.lastUserID = uid
	delete(f.followed, id)
	return nil
}

func (f *fakeMovieStore) ListFollowed(_ context.Context, uid int64, lang string) ([]FollowedMovieItem, error) {
	f.lastLang = lang
	f.lastUserID = uid
	return f.listItems, f.listErr
}

type recordingMovieEnricher struct {
	calls []domain.MovieID
}

func (r *recordingMovieEnricher) EnqueueMovieHot(id domain.MovieID) {
	r.calls = append(r.calls, id)
}

func newMovieUC(t *testing.T, reader MovieReader, store MovieFollowStore, enr MovieEnricher) *MovieFollowUseCase {
	t.Helper()
	uc, err := NewMovieFollowUseCase(reader, store, enr, nil)
	require.NoError(t, err)
	return uc
}

func TestNewMovieFollowUseCase_RequiresDeps(t *testing.T) {
	t.Parallel()
	_, err := NewMovieFollowUseCase(nil, newFakeMovieStore(), nil, nil)
	require.Error(t, err)
	_, err = NewMovieFollowUseCase(&fakeMovieReader{}, nil, nil, nil)
	require.Error(t, err)
}

func TestMovieFollow_PersistsAndEnqueues(t *testing.T) {
	t.Parallel()
	reader := &fakeMovieReader{canon: movie.Canon{ID: 42, Hydration: movie.HydrationStub}}
	store := newFakeMovieStore()
	enr := &recordingMovieEnricher{}
	uc := newMovieUC(t, reader, store, enr)

	require.NoError(t, uc.Follow(context.Background(), 7, domain.TMDBID(550)))

	assert.Equal(t, domain.TMDBID(550), reader.last)
	assert.True(t, store.followed[42])
	assert.Equal(t, int64(7), store.lastUserID)
	require.Len(t, enr.calls, 1)
	assert.Equal(t, domain.MovieID(42), enr.calls[0])
}

func TestMovieFollow_NilEnricherIsSafe(t *testing.T) {
	t.Parallel()
	reader := &fakeMovieReader{canon: movie.Canon{ID: 9}}
	store := newFakeMovieStore()
	uc := newMovieUC(t, reader, store, nil)

	require.NoError(t, uc.Follow(context.Background(), 1, domain.TMDBID(11)))
	assert.True(t, store.followed[9])
}

func TestMovieFollow_ValidatesInput(t *testing.T) {
	t.Parallel()
	reader := &fakeMovieReader{canon: movie.Canon{ID: 1}}
	store := newFakeMovieStore()
	uc := newMovieUC(t, reader, store, nil)
	ctx := context.Background()

	assert.ErrorIs(t, uc.Follow(ctx, 0, domain.TMDBID(5)), ErrInvalidUser)
	assert.ErrorIs(t, uc.Follow(ctx, 1, domain.TMDBID(0)), ErrInvalidTMDBID)
	assert.ErrorIs(t, uc.Unfollow(ctx, 0, domain.TMDBID(5)), ErrInvalidUser)
	assert.ErrorIs(t, uc.Unfollow(ctx, 1, domain.TMDBID(-3)), ErrInvalidTMDBID)
	assert.Equal(t, 0, store.followN)
	assert.Equal(t, 0, store.unfollowN)
}

func TestMovieFollow_UnknownTMDBIDIs404(t *testing.T) {
	t.Parallel()
	reader := &fakeMovieReader{err: ports.ErrNotFound}
	store := newFakeMovieStore()
	uc := newMovieUC(t, reader, store, nil)

	assert.ErrorIs(t, uc.Follow(context.Background(), 1, domain.TMDBID(999)), ErrMovieNotFound)
	assert.Equal(t, 0, store.followN)
}

func TestMovieFollow_ReaderErrorWraps(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")
	reader := &fakeMovieReader{err: boom}
	uc := newMovieUC(t, reader, newFakeMovieStore(), nil)

	err := uc.Follow(context.Background(), 1, domain.TMDBID(3))
	require.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrMovieNotFound)
}

func TestMovieUnfollow_UnknownTMDBIDIsNoOpSuccess(t *testing.T) {
	t.Parallel()
	reader := &fakeMovieReader{err: ports.ErrNotFound}
	store := newFakeMovieStore()
	uc := newMovieUC(t, reader, store, nil)

	require.NoError(t, uc.Unfollow(context.Background(), 1, domain.TMDBID(999)))
	assert.Equal(t, 0, store.unfollowN)
}

func TestMovieUnfollow_DeletesRow(t *testing.T) {
	t.Parallel()
	reader := &fakeMovieReader{canon: movie.Canon{ID: 5}}
	store := newFakeMovieStore()
	store.followed[5] = true
	uc := newMovieUC(t, reader, store, nil)

	require.NoError(t, uc.Unfollow(context.Background(), 3, domain.TMDBID(77)))
	assert.Equal(t, 1, store.unfollowN)
	assert.False(t, store.followed[5])
}

func TestMovieListFollowed_PassesLangAndUser(t *testing.T) {
	t.Parallel()
	store := newFakeMovieStore()
	store.listItems = []FollowedMovieItem{{MovieID: 1, Title: "Fight Club", FollowedAt: time.Now().UTC()}}
	uc := newMovieUC(t, &fakeMovieReader{}, store, nil)

	items, err := uc.ListFollowed(context.Background(), 4, "ru-RU")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "ru-RU", store.lastLang)
	assert.Equal(t, int64(4), store.lastUserID)
}

func TestMovieListFollowed_RejectsAnonymous(t *testing.T) {
	t.Parallel()
	uc := newMovieUC(t, &fakeMovieReader{}, newFakeMovieStore(), nil)
	_, err := uc.ListFollowed(context.Background(), 0, "en-US")
	assert.ErrorIs(t, err, ErrInvalidUser)
}

func TestMovieListFollowed_StoreErrorWraps(t *testing.T) {
	t.Parallel()
	boom := errors.New("scan failed")
	store := newFakeMovieStore()
	store.listErr = boom
	uc := newMovieUC(t, &fakeMovieReader{}, store, nil)

	_, err := uc.ListFollowed(context.Background(), 1, "en-US")
	assert.ErrorIs(t, err, boom)
}
