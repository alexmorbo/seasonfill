package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	searchapp "github.com/alexmorbo/seasonfill/internal/search/app"
	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeRepo satisfies app.LibrarySearchRepository. Records the last args and
// returns canned results/errors.
type fakeRepo struct {
	seriesFn      func(ctx context.Context, q, language string, limit int) ([]searchdomain.SeriesHit, error)
	moviesFn      func(ctx context.Context, q, language string, limit int) ([]searchdomain.MovieHit, error)
	collectionsFn func(ctx context.Context, q, language string, limit int) ([]searchdomain.CollectionHit, error)
	peopleFn      func(ctx context.Context, q, language string, limit int) ([]searchdomain.PersonHit, error)
	gotQ          string
	gotLang       string
	gotLimit      int
}

func (f *fakeRepo) SearchSeries(ctx context.Context, q, language string, limit int) ([]searchdomain.SeriesHit, error) {
	f.gotQ, f.gotLang, f.gotLimit = q, language, limit
	if f.seriesFn != nil {
		return f.seriesFn(ctx, q, language, limit)
	}
	return nil, nil
}

func (f *fakeRepo) SearchMovies(ctx context.Context, q, language string, limit int) ([]searchdomain.MovieHit, error) {
	if f.moviesFn != nil {
		return f.moviesFn(ctx, q, language, limit)
	}
	return nil, nil
}

func (f *fakeRepo) SearchCollections(ctx context.Context, q, language string, limit int) ([]searchdomain.CollectionHit, error) {
	if f.collectionsFn != nil {
		return f.collectionsFn(ctx, q, language, limit)
	}
	return nil, nil
}

func (f *fakeRepo) SearchPeople(ctx context.Context, q, language string, limit int) ([]searchdomain.PersonHit, error) {
	if f.peopleFn != nil {
		return f.peopleFn(ctx, q, language, limit)
	}
	return nil, nil
}

// fakeCatalog satisfies app.CatalogSearchRepository. Returns a canned result.
type fakeCatalog struct {
	result   searchdomain.LibrarySearchResult
	err      error
	calls    int
	gotTypes searchapp.TypeFilter
}

func (f *fakeCatalog) SearchCatalog(_ context.Context, _, _ string, _ int, types searchapp.TypeFilter) (searchdomain.LibrarySearchResult, error) {
	f.calls++
	f.gotTypes = types
	return f.result, f.err
}

// newUC builds the use case with a no-op catalog (library-path tests).
func newUC(repo searchapp.LibrarySearchRepository) *searchapp.UnifiedSearchUseCase {
	return searchapp.NewUnifiedSearchUseCase(repo, &fakeCatalog{})
}

func TestNewUnifiedSearchUseCase_NilReposPanic(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { searchapp.NewUnifiedSearchUseCase(nil, &fakeCatalog{}) })
	assert.Panics(t, func() { searchapp.NewUnifiedSearchUseCase(&fakeRepo{}, nil) })
}

func TestSearchLibrary_EmptyQueryShortCircuits(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{
		seriesFn: func(context.Context, string, string, int) ([]searchdomain.SeriesHit, error) {
			t.Fatalf("repo must not be called for empty query")
			return nil, nil
		},
	}
	uc := newUC(repo)

	for _, q := range []string{"", "   ", "\t\n"} {
		res, err := uc.SearchLibrary(context.Background(), q, "en-US", 20)
		require.NoError(t, err)
		assert.True(t, res.IsEmpty())
	}
}

func TestSearchLibrary_TrimsAndDefaultsLimit(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	uc := newUC(repo)

	_, err := uc.SearchLibrary(context.Background(), "  matrix  ", "ru-RU", 0)
	require.NoError(t, err)
	assert.Equal(t, "matrix", repo.gotQ)
	assert.Equal(t, "ru-RU", repo.gotLang)
	assert.Equal(t, 20, repo.gotLimit) // <=0 defaults to 20
}

func TestSearchLibrary_GroupsResults(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{
		seriesFn: func(context.Context, string, string, int) ([]searchdomain.SeriesHit, error) {
			return []searchdomain.SeriesHit{{SeriesID: shareddomain.SeriesID(1), Title: "The Wire"}}, nil
		},
		moviesFn: func(context.Context, string, string, int) ([]searchdomain.MovieHit, error) {
			return []searchdomain.MovieHit{{MovieID: shareddomain.MovieID(2), Title: "Heat"}}, nil
		},
		collectionsFn: func(context.Context, string, string, int) ([]searchdomain.CollectionHit, error) {
			return []searchdomain.CollectionHit{{CollectionID: searchdomain.CollectionID(3), Name: "Heat Collection"}}, nil
		},
		peopleFn: func(context.Context, string, string, int) ([]searchdomain.PersonHit, error) {
			return []searchdomain.PersonHit{{PersonID: searchdomain.PersonID(4), Name: "Al Pacino"}}, nil
		},
	}
	uc := newUC(repo)

	res, err := uc.SearchLibrary(context.Background(), "wire", "en-US", 5)
	require.NoError(t, err)
	require.Len(t, res.Series, 1)
	require.Len(t, res.Movies, 1)
	require.Len(t, res.Collections, 1)
	require.Len(t, res.People, 1)
	assert.Equal(t, "The Wire", res.Series[0].Title)
	assert.Equal(t, "Heat", res.Movies[0].Title)
	assert.Equal(t, "Heat Collection", res.Collections[0].Name)
	assert.Equal(t, "Al Pacino", res.People[0].Name)
}

func TestSearchLibrary_SeriesErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	repo := &fakeRepo{
		seriesFn: func(context.Context, string, string, int) ([]searchdomain.SeriesHit, error) {
			return nil, sentinel
		},
	}
	uc := newUC(repo)

	_, err := uc.SearchLibrary(context.Background(), "q", "en-US", 5)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "series")
}

func TestSearchLibrary_MovieErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("kaboom")
	repo := &fakeRepo{
		moviesFn: func(context.Context, string, string, int) ([]searchdomain.MovieHit, error) {
			return nil, sentinel
		},
	}
	uc := newUC(repo)

	_, err := uc.SearchLibrary(context.Background(), "q", "en-US", 5)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "movies")
}

func TestSearchLibrary_CollectionsErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("coll-boom")
	repo := &fakeRepo{
		collectionsFn: func(context.Context, string, string, int) ([]searchdomain.CollectionHit, error) {
			return nil, sentinel
		},
	}
	uc := newUC(repo)

	_, err := uc.SearchLibrary(context.Background(), "q", "en-US", 5)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "collections")
}

func TestSearchLibrary_PeopleErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("people-boom")
	repo := &fakeRepo{
		peopleFn: func(context.Context, string, string, int) ([]searchdomain.PersonHit, error) {
			return nil, sentinel
		},
	}
	uc := newUC(repo)

	_, err := uc.SearchLibrary(context.Background(), "q", "en-US", 5)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "people")
}

func libSeries(id int64, tmdb int) searchdomain.SeriesHit {
	t := shareddomain.TMDBID(tmdb)
	return searchdomain.SeriesHit{SeriesID: shareddomain.SeriesID(id), TMDBID: &t, Title: "lib", Source: searchdomain.SourceLibrary}
}
func catSeries(tmdb int, title string) searchdomain.SeriesHit {
	t := shareddomain.TMDBID(tmdb)
	return searchdomain.SeriesHit{TMDBID: &t, Title: title, Source: searchdomain.SourceCatalog}
}
func catMovie(tmdb int) searchdomain.MovieHit {
	t := shareddomain.TMDBID(tmdb)
	return searchdomain.MovieHit{TMDBID: &t, Title: "m", Source: searchdomain.SourceCatalog}
}

// (a) catalog item whose tmdb_id IS in library → dropped.
// (b) catalog item whose tmdb_id NOT in library → kept.
// (c) library-first ordering.
func TestSearch_ScopeAll_DedupAndOrder(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{seriesFn: func(context.Context, string, string, int) ([]searchdomain.SeriesHit, error) {
		return []searchdomain.SeriesHit{libSeries(11, 1399)}, nil
	}}
	cat := &fakeCatalog{result: searchdomain.LibrarySearchResult{Series: []searchdomain.SeriesHit{
		catSeries(1399, "dup-drop"), // (a) same tmdb as library → dropped
		catSeries(999, "keep"),      // (b) new tmdb → kept
	}}}
	uc := searchapp.NewUnifiedSearchUseCase(repo, cat)

	res, err := uc.Search(context.Background(), "q", "en-US", 20, searchapp.ScopeAll, searchapp.AllTypes())
	require.NoError(t, err)
	require.Len(t, res.Series, 2)
	assert.Equal(t, searchdomain.SourceLibrary, res.Series[0].Source) // (c) library first
	assert.Equal(t, "keep", res.Series[1].Title)
	assert.Equal(t, searchdomain.SourceCatalog, res.Series[1].Source)
}

// (d) same tmdb_id across different types is NOT cross-dropped.
func TestSearch_ScopeAll_DedupIsPerType(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{seriesFn: func(context.Context, string, string, int) ([]searchdomain.SeriesHit, error) {
		return []searchdomain.SeriesHit{libSeries(11, 603)}, nil // series tmdb 603
	}}
	cat := &fakeCatalog{result: searchdomain.LibrarySearchResult{
		Movies: []searchdomain.MovieHit{catMovie(603)}, // movie tmdb 603 — must survive
	}}
	uc := searchapp.NewUnifiedSearchUseCase(repo, cat)

	res, err := uc.Search(context.Background(), "q", "en-US", 20, searchapp.ScopeAll, searchapp.AllTypes())
	require.NoError(t, err)
	require.Len(t, res.Movies, 1, "movie 603 must not be dropped by series 603")
}

// (e) scope=library never touches catalog.
func TestSearch_ScopeLibrary_SkipsCatalog(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	cat := &fakeCatalog{}
	uc := searchapp.NewUnifiedSearchUseCase(repo, cat)

	_, err := uc.Search(context.Background(), "q", "en-US", 20, searchapp.ScopeLibrary, searchapp.AllTypes())
	require.NoError(t, err)
	assert.Equal(t, 0, cat.calls, "library scope must not call catalog")
}

// scope=catalog returns catalog only + forwards the type filter.
func TestSearch_ScopeCatalog_Only(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{seriesFn: func(context.Context, string, string, int) ([]searchdomain.SeriesHit, error) {
		return []searchdomain.SeriesHit{libSeries(11, 1)}, nil
	}}
	cat := &fakeCatalog{result: searchdomain.LibrarySearchResult{Movies: []searchdomain.MovieHit{catMovie(7)}}}
	uc := searchapp.NewUnifiedSearchUseCase(repo, cat)

	res, err := uc.Search(context.Background(), "q", "en-US", 20, searchapp.ScopeCatalog, searchapp.TypeFilter{Movie: true})
	require.NoError(t, err)
	assert.Empty(t, res.Series, "no library hits in catalog scope")
	require.Len(t, res.Movies, 1)
	assert.Equal(t, 1, cat.calls)
	assert.True(t, cat.gotTypes.Movie)
	assert.False(t, cat.gotTypes.Series)
}

// (f) one catalog group failing (adapter already degraded it to empty) does
// not fail the overall ScopeAll search — the use case sees a partial result +
// nil error from SearchCatalog and merges it. (Adapter-level degrade is proven
// in adapter_test; here we prove the use case tolerates a partial catalog.)
func TestSearch_ScopeAll_PartialCatalogStill200(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{moviesFn: func(context.Context, string, string, int) ([]searchdomain.MovieHit, error) {
		return []searchdomain.MovieHit{{MovieID: shareddomain.MovieID(1), Title: "lib", Source: searchdomain.SourceLibrary}}, nil
	}}
	// catalog returns only movies populated (series group "failed" → empty), nil err.
	cat := &fakeCatalog{result: searchdomain.LibrarySearchResult{Movies: []searchdomain.MovieHit{catMovie(42)}}}
	uc := searchapp.NewUnifiedSearchUseCase(repo, cat)

	res, err := uc.Search(context.Background(), "q", "en-US", 20, searchapp.ScopeAll, searchapp.AllTypes())
	require.NoError(t, err)
	assert.Empty(t, res.Series)
	assert.Len(t, res.Movies, 2) // lib + catalog movie
}
