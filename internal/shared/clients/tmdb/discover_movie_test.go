package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const movieListFixture = `{
  "page": 1,
  "total_pages": 5,
  "total_results": 100,
  "results": [
    {"id": 693134, "title": "Dune: Part Two", "original_title": "Dune: Part Two",
     "original_language": "en", "overview": "…", "poster_path": "/p.jpg",
     "backdrop_path": "/b.jpg", "release_date": "2024-02-27",
     "vote_average": 8.2, "vote_count": 5000, "popularity": 900.5,
     "genre_ids": [878, 12], "adult": false},
    {"id": 155, "title": "The Dark Knight", "original_title": "The Dark Knight",
     "original_language": "en", "release_date": "2008-07-16", "vote_average": 8.5}
  ]
}`

// movieListSrv returns an httptest server capturing the request path + query,
// echoing the shared movie-list fixture.
func movieListSrv(t *testing.T, seen *struct{ path, lang, page, query string }) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.path = r.URL.Path
		seen.lang = r.URL.Query().Get("language")
		seen.page = r.URL.Query().Get("page")
		seen.query = r.URL.RawQuery
		_, _ = w.Write([]byte(movieListFixture))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_TrendingMovie_HappyPath(t *testing.T) {
	var seen struct{ path, lang, page, query string }
	srv := movieListSrv(t, &seen)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.TrendingMovie(context.Background(), TrendingWeek, "ru-RU", 2)
	require.NoError(t, err)
	assert.Equal(t, "/trending/movie/week", seen.path)
	assert.Equal(t, "ru-RU", seen.lang)
	assert.Equal(t, "2", seen.page)
	require.Len(t, resp.Results, 2)
	assert.Equal(t, int64(693134), resp.Results[0].ID)
	assert.Equal(t, "Dune: Part Two", resp.Results[0].Title)
	assert.Equal(t, "2024-02-27", resp.Results[0].ReleaseDate)
}

func TestClient_TrendingMovie_InvalidScope(t *testing.T) {
	c := mustNew(t, "http://127.0.0.1:0", "tk")
	defer c.Close()
	_, err := c.TrendingMovie(context.Background(), TrendingScope("month"), "en-US", 1)
	require.Error(t, err)
}

func TestClient_MoviePopular_HappyPath(t *testing.T) {
	var seen struct{ path, lang, page, query string }
	srv := movieListSrv(t, &seen)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	_, err := c.MoviePopular(context.Background(), "en-US", 1)
	require.NoError(t, err)
	assert.Equal(t, "/movie/popular", seen.path)
	assert.Equal(t, "en-US", seen.lang)
}

// TestClient_MovieMethods_EmptyLangDefaults proves every movie list method
// honors c.languageFor — an empty lang falls back to the client default
// (issue #1184 must NOT reappear).
func TestClient_MovieMethods_EmptyLangDefaults(t *testing.T) {
	var seen struct{ path, lang, page, query string }
	srv := movieListSrv(t, &seen)
	c := mustNew(t, srv.URL, "tk") // client default lang = en-US
	defer c.Close()

	cases := []struct {
		name string
		call func() error
		path string
	}{
		{"discover", func() error { _, e := c.DiscoverMovie(context.Background(), MovieDiscoverFilter{}, "", 1); return e }, "/discover/movie"},
		{"trending", func() error { _, e := c.TrendingMovie(context.Background(), TrendingDay, "", 1); return e }, "/trending/movie/day"},
		{"popular", func() error { _, e := c.MoviePopular(context.Background(), "", 1); return e }, "/movie/popular"},
		{"search", func() error { _, e := c.SearchMovie(context.Background(), "dune", "", 1); return e }, "/search/movie"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.call())
			assert.Equal(t, tc.path, seen.path)
			assert.Equal(t, "en-US", seen.lang, "empty lang must fall back to client default, not blank (#1184)")
		})
	}
}

func TestClient_SearchMovie_EmptyQuery(t *testing.T) {
	c := mustNew(t, "http://127.0.0.1:0", "tk")
	defer c.Close()
	_, err := c.SearchMovie(context.Background(), "   ", "en-US", 1)
	require.Error(t, err)
}

func TestClient_SearchMovie_PassesQueryAndAdult(t *testing.T) {
	var seen struct{ path, lang, page, query string }
	srv := movieListSrv(t, &seen)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	_, err := c.SearchMovie(context.Background(), "dune part two", "de-DE", 1)
	require.NoError(t, err)
	assert.Equal(t, "/search/movie", seen.path)
	assert.Contains(t, seen.query, "query=dune+part+two")
	assert.Contains(t, seen.query, "include_adult=false")
	assert.Equal(t, "de-DE", seen.lang)
}

func TestBuildMovieDiscoverQuery_GoldenTable(t *testing.T) {
	gte := "2016-01-01"
	lte := "2026-12-31"
	yr := 2024
	va := 7.5
	lang := "ja"
	region := "US"

	cases := []struct {
		name   string
		filter MovieDiscoverFilter
		checks map[string]string // exact param=value expectations
		absent []string          // params that must NOT be present
	}{
		{
			name:   "empty omits everything but base",
			filter: MovieDiscoverFilter{},
			checks: map[string]string{"language": "en-US", "page": "1", "include_adult": "false"},
			absent: []string{"with_genres", "sort_by", "primary_release_year", "with_release_type"},
		},
		{
			name: "full filter",
			filter: MovieDiscoverFilter{
				WithGenres:            []int{878, 12},
				WithoutGenres:         []int{27},
				PrimaryReleaseDateGte: &gte,
				PrimaryReleaseDateLte: &lte,
				PrimaryReleaseYear:    &yr,
				VoteAverageGte:        &va,
				WithOriginalLang:      &lang,
				WatchRegion:           &region,
				WithReleaseType:       []int{3, 2},
				SortBy:                "revenue.desc",
			},
			checks: map[string]string{
				"with_genres":              "878,12",
				"without_genres":           "27",
				"primary_release_date.gte": "2016-01-01",
				"primary_release_date.lte": "2026-12-31",
				"primary_release_year":     "2024",
				"vote_average.gte":         "7.5",
				"with_original_language":   "ja",
				"watch_region":             "US",
				"with_release_type":        "3|2",
				"sort_by":                  "revenue.desc",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := buildMovieDiscoverQuery(tc.filter, "en-US", 1)
			for k, want := range tc.checks {
				assert.Equal(t, want, q.Get(k), "param %q", k)
			}
			for _, k := range tc.absent {
				assert.Empty(t, q.Get(k), "param %q must be absent", k)
			}
		})
	}
}

func TestBuildMovieDiscoverQuery_PageClampedToOne(t *testing.T) {
	q := buildMovieDiscoverQuery(MovieDiscoverFilter{}, "en-US", 0)
	assert.Equal(t, "1", q.Get("page"))
}
