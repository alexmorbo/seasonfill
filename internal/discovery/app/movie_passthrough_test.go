package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeMovieClient scripts the four movie list methods. Only DiscoverMovie /
// TrendingMovie are exercised by the passthrough tests; the rest return the
// same scripted response.
type fakeMovieClient struct {
	resp *tmdb.MovieListResponse
	err  error
	// seenLang captures the language argument of the last call (for #1184).
	seenLang string
}

func (f *fakeMovieClient) DiscoverMovie(_ context.Context, _ tmdb.MovieDiscoverFilter, lang string, _ int) (*tmdb.MovieListResponse, error) {
	f.seenLang = lang
	return f.resp, f.err
}

func (f *fakeMovieClient) TrendingMovie(_ context.Context, _ tmdb.TrendingScope, lang string, _ int) (*tmdb.MovieListResponse, error) {
	f.seenLang = lang
	return f.resp, f.err
}

func (f *fakeMovieClient) MoviePopular(_ context.Context, lang string, _ int) (*tmdb.MovieListResponse, error) {
	f.seenLang = lang
	return f.resp, f.err
}

func (f *fakeMovieClient) SearchMovie(_ context.Context, _ string, lang string, _ int) (*tmdb.MovieListResponse, error) {
	f.seenLang = lang
	return f.resp, f.err
}

// fakeMovieStubs records every EnsureMovieStub call and can fail on a chosen
// tmdb_id.
type fakeMovieStubs struct {
	calls    []stubCall
	failTMDB int64
}

type stubCall struct {
	tmdbID   int64
	lang     string
	title    string
	origTtl  string
	origLang string
}

func (s *fakeMovieStubs) EnsureMovieStub(_ context.Context, tmdbID shareddomain.TMDBID, lang, title, originalTitle, originalLanguage string, _, _ *string) (shareddomain.MovieID, error) {
	s.calls = append(s.calls, stubCall{
		tmdbID: int64(tmdbID), lang: lang, title: title, origTtl: originalTitle, origLang: originalLanguage,
	})
	if int64(tmdbID) == s.failTMDB {
		return 0, errors.New("boom")
	}
	return shareddomain.MovieID(tmdbID), nil
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func twoMovieResp() *tmdb.MovieListResponse {
	return &tmdb.MovieListResponse{
		Page: 1,
		Results: []tmdb.MovieListEntry{
			{ID: 693134, Title: "Dune: Part Two", OriginalTitle: "Dune: Part Two", OriginalLanguage: "en", ReleaseDate: "2024-02-27", VoteAverage: 8.2, PosterPath: "/p.jpg"},
			{ID: 155, Title: "The Dark Knight", OriginalLanguage: "en", ReleaseDate: "2008-07-16"},
		},
	}
}

func TestMoviePassthrough_FetchDiscover_StubUpsertsEveryRow(t *testing.T) {
	client := &fakeMovieClient{resp: twoMovieResp()}
	stubs := &fakeMovieStubs{}
	p := NewMovieTMDBPassthrough(client, stubs, testLog())

	items, err := p.FetchDiscover(context.Background(), tmdb.MovieDiscoverFilter{}, "en-US", 1)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Len(t, stubs.calls, 2)
	assert.Equal(t, "Dune: Part Two", items[0].Title)
	require.NotNil(t, items[0].Year)
	assert.Equal(t, 2024, *items[0].Year)
	require.NotNil(t, items[0].TMDBRating)
	assert.InDelta(t, 8.2, *items[0].TMDBRating, 0.001)
}

// TestMoviePassthrough_LangIsRequestLang is the #1184 guard: the request lang
// flows verbatim into both the TMDB call and EnsureMovieStub — never blanked
// or defaulted by the passthrough.
func TestMoviePassthrough_LangIsRequestLang(t *testing.T) {
	client := &fakeMovieClient{resp: twoMovieResp()}
	stubs := &fakeMovieStubs{}
	p := NewMovieTMDBPassthrough(client, stubs, testLog())

	_, err := p.FetchDiscover(context.Background(), tmdb.MovieDiscoverFilter{}, "ru-RU", 1)
	require.NoError(t, err)
	assert.Equal(t, "ru-RU", client.seenLang)
	require.NotEmpty(t, stubs.calls)
	for _, c := range stubs.calls {
		assert.Equal(t, "ru-RU", c.lang, "stub seeded under request lang, not default (#1184)")
	}
}

func TestMoviePassthrough_StubErrorDropsRowKeepsRest(t *testing.T) {
	client := &fakeMovieClient{resp: twoMovieResp()}
	stubs := &fakeMovieStubs{failTMDB: 693134} // first row fails
	p := NewMovieTMDBPassthrough(client, stubs, testLog())

	items, err := p.FetchDiscover(context.Background(), tmdb.MovieDiscoverFilter{}, "en-US", 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(155), int64(*items[0].TMDBID))
}

func TestMoviePassthrough_EmptyResponseNil(t *testing.T) {
	client := &fakeMovieClient{resp: &tmdb.MovieListResponse{}}
	p := NewMovieTMDBPassthrough(client, &fakeMovieStubs{}, testLog())
	items, err := p.FetchTrending(context.Background(), tmdb.TrendingDay, "en-US", 1)
	require.NoError(t, err)
	assert.Nil(t, items)
}

func TestMoviePassthrough_TransportErrorWrapped(t *testing.T) {
	client := &fakeMovieClient{err: errors.New("net down")}
	p := NewMovieTMDBPassthrough(client, &fakeMovieStubs{}, testLog())
	_, err := p.FetchPopular(context.Background(), "en-US", 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMovieTMDBUnavailable)
}

func TestMoviePassthrough_SkipsMalformedRows(t *testing.T) {
	client := &fakeMovieClient{resp: &tmdb.MovieListResponse{Results: []tmdb.MovieListEntry{
		{ID: 0, Title: "no id"},
		{ID: 5, Title: ""},
		{ID: 7, Title: "ok"},
	}}}
	stubs := &fakeMovieStubs{}
	p := NewMovieTMDBPassthrough(client, stubs, testLog())
	items, err := p.FetchSearch(context.Background(), "q", "en-US", 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "ok", items[0].Title)
}
