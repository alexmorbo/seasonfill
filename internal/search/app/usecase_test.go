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
	seriesFn func(ctx context.Context, q, language string, limit int) ([]searchdomain.SeriesHit, error)
	moviesFn func(ctx context.Context, q, language string, limit int) ([]searchdomain.MovieHit, error)
	gotQ     string
	gotLang  string
	gotLimit int
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

func TestNewUnifiedSearchUseCase_NilRepoPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { searchapp.NewUnifiedSearchUseCase(nil) })
}

func TestSearchLibrary_EmptyQueryShortCircuits(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{
		seriesFn: func(context.Context, string, string, int) ([]searchdomain.SeriesHit, error) {
			t.Fatalf("repo must not be called for empty query")
			return nil, nil
		},
	}
	uc := searchapp.NewUnifiedSearchUseCase(repo)

	for _, q := range []string{"", "   ", "\t\n"} {
		res, err := uc.SearchLibrary(context.Background(), q, "en-US", 20)
		require.NoError(t, err)
		assert.True(t, res.IsEmpty())
	}
}

func TestSearchLibrary_TrimsAndDefaultsLimit(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	uc := searchapp.NewUnifiedSearchUseCase(repo)

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
	}
	uc := searchapp.NewUnifiedSearchUseCase(repo)

	res, err := uc.SearchLibrary(context.Background(), "wire", "en-US", 5)
	require.NoError(t, err)
	require.Len(t, res.Series, 1)
	require.Len(t, res.Movies, 1)
	assert.Equal(t, "The Wire", res.Series[0].Title)
	assert.Equal(t, "Heat", res.Movies[0].Title)
	assert.Empty(t, res.Collections)
	assert.Empty(t, res.People)
}

func TestSearchLibrary_SeriesErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	repo := &fakeRepo{
		seriesFn: func(context.Context, string, string, int) ([]searchdomain.SeriesHit, error) {
			return nil, sentinel
		},
	}
	uc := searchapp.NewUnifiedSearchUseCase(repo)

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
	uc := searchapp.NewUnifiedSearchUseCase(repo)

	_, err := uc.SearchLibrary(context.Background(), "q", "en-US", 5)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "movies")
}
