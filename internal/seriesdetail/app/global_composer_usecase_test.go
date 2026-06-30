package seriesdetail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// stubCacheLookup implements SeriesCacheLookupPort.
type stubCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (s *stubCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entries, nil
}

func (s *stubCacheLookup) ListBySeriesIDs(_ context.Context, ids []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[domain.SeriesID][]series.CacheEntry, len(ids))
	for _, id := range ids {
		out[id] = s.entries
	}
	return out, nil
}

// fakeComposer captures the (instance, sonarr_id) tuple. Satisfies
// seriesdetail.ComposerPort.
type fakeComposer struct {
	calledInstance domain.InstanceName
	calledSonarrID domain.SonarrSeriesID
	resp           *seriesdetail.Detail
	err            error
}

func (f *fakeComposer) Get(_ context.Context, inst domain.InstanceName, sid domain.SonarrSeriesID, _ string) (*seriesdetail.Detail, error) {
	f.calledInstance = inst
	f.calledSonarrID = sid
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// fakeTMDBFallback satisfies seriesdetail.TMDBFallbackPort.
type fakeTMDBFallback struct {
	called bool
	resp   *seriesdetail.Detail
	err    error
}

func (f *fakeTMDBFallback) GetCanonical(_ context.Context, _ domain.SeriesID, _ string) (*seriesdetail.Detail, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func TestGlobalComposerUseCase_TwoInstances_PicksLexFirst(t *testing.T) {
	entry1 := series.CacheEntry{InstanceName: "beta", SonarrSeriesID: 7}
	entry2 := series.CacheEntry{InstanceName: "alpha", SonarrSeriesID: 99}
	cache := &stubCacheLookup{entries: []series.CacheEntry{entry1, entry2}}
	composer := &fakeComposer{resp: &seriesdetail.Detail{SeriesID: 140}}
	tmdb := &fakeTMDBFallback{}
	uc, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup:  cache,
		Composer:     composer,
		TMDBFallback: tmdb,
	})
	require.NoError(t, err)
	detail, err := uc.Get(context.Background(), 140, "en-US")
	require.NoError(t, err)
	assert.Equal(t, domain.InstanceName("alpha"), composer.calledInstance, "preferred is lex-first")
	assert.Equal(t, domain.SonarrSeriesID(99), composer.calledSonarrID, "sonarr_id from alpha entry")
	assert.Equal(t, []domain.InstanceName{"alpha", "beta"}, detail.InLibraryInstances)
	assert.False(t, tmdb.called, "TMDB fallback must NOT be called when cache rows exist")
}

func TestGlobalComposerUseCase_OneInstance_DelegatesAndFillsName(t *testing.T) {
	cache := &stubCacheLookup{entries: []series.CacheEntry{{InstanceName: "homelab", SonarrSeriesID: 140}}}
	composer := &fakeComposer{resp: &seriesdetail.Detail{SeriesID: 140}}
	tmdb := &fakeTMDBFallback{}
	uc, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup: cache, Composer: composer, TMDBFallback: tmdb,
	})
	require.NoError(t, err)
	detail, err := uc.Get(context.Background(), 140, "en-US")
	require.NoError(t, err)
	assert.Equal(t, []domain.InstanceName{"homelab"}, detail.InLibraryInstances)
	assert.Equal(t, domain.InstanceName("homelab"), composer.calledInstance)
	assert.Equal(t, domain.SonarrSeriesID(140), composer.calledSonarrID)
}

func TestGlobalComposerUseCase_NoInstances_FallsBackToTMDB(t *testing.T) {
	cache := &stubCacheLookup{entries: nil}
	composer := &fakeComposer{}
	tmdb := &fakeTMDBFallback{resp: &seriesdetail.Detail{SeriesID: 99999, InLibraryInstances: []domain.InstanceName{}}}
	uc, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup: cache, Composer: composer, TMDBFallback: tmdb,
	})
	require.NoError(t, err)
	detail, err := uc.Get(context.Background(), 99999, "en-US")
	require.NoError(t, err)
	assert.Equal(t, domain.SeriesID(99999), detail.SeriesID)
	assert.Empty(t, detail.InLibraryInstances)
	assert.NotNil(t, detail.InLibraryInstances, "must be empty slice, not nil")
	assert.Equal(t, domain.InstanceName(""), composer.calledInstance, "Composer NOT called when no cache rows")
	assert.True(t, tmdb.called, "TMDB fallback must be called when no cache rows")
}

func TestGlobalComposerUseCase_DuplicateInstances_Collapsed(t *testing.T) {
	// Same instance with two different sonarr_ids — collapse to one entry
	// in InLibraryInstances.
	entries := []series.CacheEntry{
		{InstanceName: "alpha", SonarrSeriesID: 1},
		{InstanceName: "alpha", SonarrSeriesID: 2},
		{InstanceName: "beta", SonarrSeriesID: 7},
	}
	cache := &stubCacheLookup{entries: entries}
	composer := &fakeComposer{resp: &seriesdetail.Detail{SeriesID: 140}}
	tmdb := &fakeTMDBFallback{}
	uc, _ := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup: cache, Composer: composer, TMDBFallback: tmdb,
	})
	detail, err := uc.Get(context.Background(), 140, "en-US")
	require.NoError(t, err)
	assert.Equal(t, []domain.InstanceName{"alpha", "beta"}, detail.InLibraryInstances)
}

func TestGlobalComposerUseCase_InvalidSeriesID(t *testing.T) {
	uc, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup:  &stubCacheLookup{},
		Composer:     &fakeComposer{},
		TMDBFallback: &fakeTMDBFallback{},
	})
	require.NoError(t, err)
	_, err = uc.Get(context.Background(), 0, "en-US")
	assert.ErrorIs(t, err, ports.ErrNotFound)
	_, err = uc.Get(context.Background(), -5, "en-US")
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGlobalComposerUseCase_CacheLookupError_Propagates(t *testing.T) {
	wantErr := errors.New("db down")
	uc, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup:  &stubCacheLookup{err: wantErr},
		Composer:     &fakeComposer{},
		TMDBFallback: &fakeTMDBFallback{},
	})
	require.NoError(t, err)
	_, err = uc.Get(context.Background(), 140, "en-US")
	assert.ErrorIs(t, err, wantErr)
}

func TestGlobalComposerUseCase_ComposerError_Propagates(t *testing.T) {
	wantErr := errors.New("composer boom")
	uc, _ := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup:  &stubCacheLookup{entries: []series.CacheEntry{{InstanceName: "alpha", SonarrSeriesID: 1}}},
		Composer:     &fakeComposer{err: wantErr},
		TMDBFallback: &fakeTMDBFallback{},
	})
	_, err := uc.Get(context.Background(), 140, "en-US")
	assert.ErrorIs(t, err, wantErr)
}

func TestGlobalComposerUseCase_TMDBFallbackError_Propagates(t *testing.T) {
	wantErr := errors.New("tmdb fallback boom")
	uc, _ := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup:  &stubCacheLookup{entries: nil},
		Composer:     &fakeComposer{},
		TMDBFallback: &fakeTMDBFallback{err: wantErr},
	})
	_, err := uc.Get(context.Background(), 99999, "en-US")
	assert.ErrorIs(t, err, wantErr)
}

func TestNewGlobalComposerUseCase_NilDeps(t *testing.T) {
	_, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{})
	require.Error(t, err)

	_, err = seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup: &stubCacheLookup{},
	})
	require.Error(t, err)

	_, err = seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup: &stubCacheLookup{},
		Composer:    &fakeComposer{},
	})
	require.Error(t, err)
}
