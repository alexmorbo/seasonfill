package seriesdetail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeResolveStore is an in-memory resolve-or-create-by-tmdb store. It
// mirrors the enrichment SeriesRepository contract the use case relies
// on: GetByTMDBID returns ports.ErrNotFound on miss; UpsertStub is
// idempotent on tmdb_id (a second call returns the same id, no dup).
type fakeResolveStore struct {
	byTMDB     map[domain.TMDBID]domain.SeriesID
	nextID     domain.SeriesID
	getErr     error // forced GetByTMDBID error (non-NotFound)
	upsertErr  error // forced UpsertStub error
	getCalls   int
	upsertRows []series.Canon // every UpsertStub arg, in order
}

func newFakeResolveStore() *fakeResolveStore {
	return &fakeResolveStore{byTMDB: map[domain.TMDBID]domain.SeriesID{}, nextID: 100}
}

func (f *fakeResolveStore) GetByTMDBID(_ context.Context, tmdbID domain.TMDBID) (series.Canon, error) {
	f.getCalls++
	if f.getErr != nil {
		return series.Canon{}, f.getErr
	}
	if id, ok := f.byTMDB[tmdbID]; ok {
		tid := tmdbID
		return series.Canon{ID: id, TMDBID: &tid, Hydration: series.HydrationFull}, nil
	}
	return series.Canon{}, ports.ErrNotFound
}

func (f *fakeResolveStore) UpsertStub(_ context.Context, c series.Canon) (domain.SeriesID, error) {
	f.upsertRows = append(f.upsertRows, c)
	if f.upsertErr != nil {
		return 0, f.upsertErr
	}
	if c.TMDBID == nil {
		return 0, errors.New("fake upsert stub: tmdb_id required") //nolint:err113
	}
	if id, ok := f.byTMDB[*c.TMDBID]; ok {
		return id, nil
	}
	id := f.nextID
	f.nextID++
	f.byTMDB[*c.TMDBID] = id
	return id, nil
}

type fakeResolveEnricher struct {
	calls []struct {
		id domain.SeriesID
		h  series.Hydration
	}
}

func (f *fakeResolveEnricher) EnqueueIfStale(id domain.SeriesID, h series.Hydration) {
	f.calls = append(f.calls, struct {
		id domain.SeriesID
		h  series.Hydration
	}{id, h})
}

func quietResolveLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestResolveByTMDB_ExistingCanon_ReturnsID_NoWrite(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	store.byTMDB[1399] = 42
	enr := &fakeResolveEnricher{}
	uc, err := NewResolveUseCase(store, enr, quietResolveLogger())
	require.NoError(t, err)

	id, err := uc.ResolveByTMDB(context.Background(), 1399)
	require.NoError(t, err)
	assert.Equal(t, domain.SeriesID(42), id)
	assert.Empty(t, store.upsertRows, "existing canon must not be written")
	assert.Empty(t, enr.calls, "existing canon must not enqueue enrichment")
}

func TestResolveByTMDB_UnknownTMDB_CreatesStub_Enqueues(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	enr := &fakeResolveEnricher{}
	uc, err := NewResolveUseCase(store, enr, quietResolveLogger())
	require.NoError(t, err)

	id, err := uc.ResolveByTMDB(context.Background(), 555)
	require.NoError(t, err)
	assert.Equal(t, domain.SeriesID(100), id)
	require.Len(t, store.upsertRows, 1, "exactly one stub created")
	require.NotNil(t, store.upsertRows[0].TMDBID)
	assert.Equal(t, domain.TMDBID(555), *store.upsertRows[0].TMDBID)
	assert.Equal(t, series.HydrationStub, store.upsertRows[0].Hydration)
	require.Len(t, enr.calls, 1, "enrichment enqueued exactly once")
	assert.Equal(t, domain.SeriesID(100), enr.calls[0].id)
	assert.Equal(t, series.HydrationStub, enr.calls[0].h)
}

func TestResolveByTMDB_SecondCall_Idempotent(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	enr := &fakeResolveEnricher{}
	uc, err := NewResolveUseCase(store, enr, quietResolveLogger())
	require.NoError(t, err)

	first, err := uc.ResolveByTMDB(context.Background(), 777)
	require.NoError(t, err)
	second, err := uc.ResolveByTMDB(context.Background(), 777)
	require.NoError(t, err)

	assert.Equal(t, first, second, "same id on repeat")
	assert.Len(t, store.upsertRows, 1, "second call takes the existing-row branch — no duplicate write")
	assert.Len(t, enr.calls, 1, "second call does not re-enqueue")
}

func TestResolveByTMDB_InvalidTMDB_Returns400Sentinel(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	uc, err := NewResolveUseCase(store, &fakeResolveEnricher{}, quietResolveLogger())
	require.NoError(t, err)

	for _, bad := range []domain.TMDBID{0, -5} {
		_, rerr := uc.ResolveByTMDB(context.Background(), bad)
		assert.ErrorIs(t, rerr, ErrInvalidTMDBID, "tmdb=%d", bad)
	}
	assert.Equal(t, 0, store.getCalls, "invalid input never touches the store")
	assert.Empty(t, store.upsertRows)
}

func TestResolveByTMDB_LookupError_Wrapped(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	store.getErr = errors.New("db down") //nolint:err113
	uc, err := NewResolveUseCase(store, &fakeResolveEnricher{}, quietResolveLogger())
	require.NoError(t, err)

	_, rerr := uc.ResolveByTMDB(context.Background(), 900)
	require.Error(t, rerr)
	assert.NotErrorIs(t, rerr, ErrInvalidTMDBID)
	assert.Empty(t, store.upsertRows, "lookup error must not fall through to a stub create")
}

func TestResolveByTMDB_NilEnricher_StillCreatesStub(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	uc, err := NewResolveUseCase(store, nil, quietResolveLogger())
	require.NoError(t, err)

	id, rerr := uc.ResolveByTMDB(context.Background(), 321)
	require.NoError(t, rerr)
	assert.Equal(t, domain.SeriesID(100), id)
	assert.Len(t, store.upsertRows, 1)
}

func TestNewResolveUseCase_NilStore(t *testing.T) {
	t.Parallel()
	_, err := NewResolveUseCase(nil, &fakeResolveEnricher{}, quietResolveLogger())
	require.Error(t, err)
}
