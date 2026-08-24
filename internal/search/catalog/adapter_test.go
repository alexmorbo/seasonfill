package catalog_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	searchapp "github.com/alexmorbo/seasonfill/internal/search/app"
	"github.com/alexmorbo/seasonfill/internal/search/catalog"
	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// stubTMDB is a mock TMDBSearchClient — NEVER hits the network. Each field is
// a canned response/error; nil funcs return empty responses.
type stubTMDB struct {
	tv     func() (*tmdb.TVListResponse, error)
	movie  func() (*tmdb.MovieListResponse, error)
	coll   func() (*tmdb.CollectionListResponse, error)
	person func() (*tmdb.PersonListResponse, error)
	calls  struct{ tv, movie, coll, person int }
}

func (s *stubTMDB) SearchTV(context.Context, string, string, int) (*tmdb.TVListResponse, error) {
	s.calls.tv++
	if s.tv != nil {
		return s.tv()
	}
	return &tmdb.TVListResponse{}, nil
}
func (s *stubTMDB) SearchMovie(context.Context, string, string, int) (*tmdb.MovieListResponse, error) {
	s.calls.movie++
	if s.movie != nil {
		return s.movie()
	}
	return &tmdb.MovieListResponse{}, nil
}
func (s *stubTMDB) SearchCollection(context.Context, string, string, int) (*tmdb.CollectionListResponse, error) {
	s.calls.coll++
	if s.coll != nil {
		return s.coll()
	}
	return &tmdb.CollectionListResponse{}, nil
}
func (s *stubTMDB) SearchPerson(context.Context, string, string, int) (*tmdb.PersonListResponse, error) {
	s.calls.person++
	if s.person != nil {
		return s.person()
	}
	return &tmdb.PersonListResponse{}, nil
}

func newAdapter(t *testing.T, c catalog.TMDBSearchClient) *catalog.Adapter {
	t.Helper()
	return catalog.NewAdapter(c, slog.Default())
}

func TestSearchCatalog_MapsAllGroups_SourceCatalog(t *testing.T) {
	t.Parallel()
	stub := &stubTMDB{
		tv: func() (*tmdb.TVListResponse, error) {
			return &tmdb.TVListResponse{Results: []tmdb.TVListEntry{
				{ID: 1399, Name: "Game of Thrones", FirstAirDate: "2011-04-17", PosterPath: "/s.jpg"},
				{ID: 0, Name: "skip-zero"}, // dropped: id<=0
				{ID: 5, Name: ""},          // dropped: empty title
			}}, nil
		},
		movie: func() (*tmdb.MovieListResponse, error) {
			return &tmdb.MovieListResponse{Results: []tmdb.MovieListEntry{
				{ID: 603, Title: "The Matrix", ReleaseDate: "1999-03-31"},
			}}, nil
		},
		coll: func() (*tmdb.CollectionListResponse, error) {
			return &tmdb.CollectionListResponse{Results: []tmdb.CollectionListEntry{
				{ID: 2344, Name: "The Matrix Collection", PosterPath: "/c.jpg"},
			}}, nil
		},
		person: func() (*tmdb.PersonListResponse, error) {
			return &tmdb.PersonListResponse{Results: []tmdb.PersonListEntry{
				{ID: 6384, Name: "Keanu Reeves", ProfilePath: "/p.jpg", KnownForDepartment: "Acting"},
			}}, nil
		},
	}
	res, err := newAdapter(t, stub).SearchCatalog(context.Background(), "matrix", "en-US", 20, searchapp.AllTypes())
	require.NoError(t, err)

	require.Len(t, res.Series, 1) // zero-id + empty-title dropped
	assert.Equal(t, int64(0), int64(res.Series[0].SeriesID), "catalog hit has no library id")
	require.NotNil(t, res.Series[0].TMDBID)
	assert.Equal(t, "Game of Thrones", res.Series[0].Title)
	require.NotNil(t, res.Series[0].Year)
	assert.Equal(t, 2011, *res.Series[0].Year)
	assert.Equal(t, searchdomain.SourceCatalog, res.Series[0].Source)

	require.Len(t, res.Movies, 1)
	assert.Equal(t, 1999, *res.Movies[0].Year)
	assert.Equal(t, searchdomain.SourceCatalog, res.Movies[0].Source)

	require.Len(t, res.Collections, 1)
	assert.Equal(t, searchdomain.SourceCatalog, res.Collections[0].Source)

	require.Len(t, res.People, 1)
	require.NotNil(t, res.People[0].KnownFor)
	assert.Equal(t, "Acting", *res.People[0].KnownFor)
	assert.Equal(t, searchdomain.SourceCatalog, res.People[0].Source)
}

func TestSearchCatalog_OneGroupFailsOthersSurvive(t *testing.T) {
	t.Parallel()
	stub := &stubTMDB{
		tv: func() (*tmdb.TVListResponse, error) { return nil, errors.New("tmdb 500") }, // fails
		movie: func() (*tmdb.MovieListResponse, error) {
			return &tmdb.MovieListResponse{Results: []tmdb.MovieListEntry{{ID: 603, Title: "The Matrix"}}}, nil
		},
	}
	res, err := newAdapter(t, stub).SearchCatalog(context.Background(), "matrix", "en-US", 20, searchapp.AllTypes())
	require.NoError(t, err, "whole search must still succeed when one group fails")
	assert.Empty(t, res.Series, "failed group degrades to empty")
	require.Len(t, res.Movies, 1, "surviving group populated")
}

func TestSearchCatalog_TypesGate_SkipsTMDBCalls(t *testing.T) {
	t.Parallel()
	stub := &stubTMDB{}
	_, err := newAdapter(t, stub).SearchCatalog(context.Background(), "q", "en-US", 20,
		searchapp.TypeFilter{Movie: true}) // only movies
	require.NoError(t, err)
	assert.Equal(t, 0, stub.calls.tv)
	assert.Equal(t, 1, stub.calls.movie)
	assert.Equal(t, 0, stub.calls.coll)
	assert.Equal(t, 0, stub.calls.person)
}

func TestSearchCatalog_LimitCapsEachGroup(t *testing.T) {
	t.Parallel()
	stub := &stubTMDB{
		movie: func() (*tmdb.MovieListResponse, error) {
			return &tmdb.MovieListResponse{Results: []tmdb.MovieListEntry{
				{ID: 1, Title: "a"}, {ID: 2, Title: "b"}, {ID: 3, Title: "c"},
			}}, nil
		},
	}
	res, err := newAdapter(t, stub).SearchCatalog(context.Background(), "q", "en-US", 2, searchapp.TypeFilter{Movie: true})
	require.NoError(t, err)
	assert.Len(t, res.Movies, 2, "limit caps the group")
}

func TestSearchCatalog_EmptyQueryShortCircuits(t *testing.T) {
	t.Parallel()
	stub := &stubTMDB{}
	res, err := newAdapter(t, stub).SearchCatalog(context.Background(), "   ", "en-US", 20, searchapp.AllTypes())
	require.NoError(t, err)
	assert.True(t, res.IsEmpty())
	assert.Equal(t, 0, stub.calls.movie, "no TMDB call on empty query")
}

func TestSearchCatalog_NilClientDegradesEmpty(t *testing.T) {
	t.Parallel()
	res, err := catalog.NewAdapter(nil, slog.Default()).
		SearchCatalog(context.Background(), "q", "en-US", 20, searchapp.AllTypes())
	require.NoError(t, err)
	assert.True(t, res.IsEmpty())
}

func TestNewAdapter_NilLogPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { catalog.NewAdapter(&stubTMDB{}, nil) })
}
