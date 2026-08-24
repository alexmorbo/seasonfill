package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const collectionListFixture = `{
  "page": 1, "total_pages": 1, "total_results": 2,
  "results": [
    {"id": 10, "name": "The Matrix Collection", "original_name": "The Matrix Collection",
     "overview": "…", "poster_path": "/c.jpg", "backdrop_path": "/cb.jpg", "adult": false},
    {"id": 0, "name": "skip-zero-id"}
  ]
}`

const personListFixture = `{
  "page": 1, "total_pages": 1, "total_results": 1,
  "results": [
    {"id": 6384, "name": "Keanu Reeves", "original_name": "Keanu Reeves",
     "profile_path": "/p.jpg", "known_for_department": "Acting", "popularity": 42.5, "adult": false}
  ]
}`

func searchSrv(t *testing.T, body string, seen *struct{ path, lang, page, query string }) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.path = r.URL.Path
		seen.lang = r.URL.Query().Get("language")
		seen.page = r.URL.Query().Get("page")
		seen.query = r.URL.RawQuery
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_SearchCollection_HappyPath(t *testing.T) {
	var seen struct{ path, lang, page, query string }
	srv := searchSrv(t, collectionListFixture, &seen)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.SearchCollection(context.Background(), "matrix", "ru-RU", 1)
	require.NoError(t, err)
	assert.Equal(t, "/search/collection", seen.path)
	assert.Equal(t, "ru-RU", seen.lang)
	assert.Equal(t, "1", seen.page)
	assert.Contains(t, seen.query, "query=matrix")
	assert.Contains(t, seen.query, "include_adult=false")
	require.Len(t, resp.Results, 2)
	assert.Equal(t, int64(10), resp.Results[0].ID)
	assert.Equal(t, "The Matrix Collection", resp.Results[0].Name)
	assert.Equal(t, "/c.jpg", resp.Results[0].PosterPath)
}

func TestClient_SearchCollection_EmptyQuery(t *testing.T) {
	c := mustNew(t, "http://127.0.0.1:0", "tk")
	defer c.Close()
	_, err := c.SearchCollection(context.Background(), "   ", "en-US", 1)
	require.Error(t, err)
}

func TestClient_SearchPerson_HappyPath(t *testing.T) {
	var seen struct{ path, lang, page, query string }
	srv := searchSrv(t, personListFixture, &seen)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.SearchPerson(context.Background(), "keanu", "de-DE", 2)
	require.NoError(t, err)
	assert.Equal(t, "/search/person", seen.path)
	assert.Equal(t, "de-DE", seen.lang)
	assert.Equal(t, "2", seen.page)
	assert.Contains(t, seen.query, "include_adult=false")
	require.Len(t, resp.Results, 1)
	assert.Equal(t, int64(6384), resp.Results[0].ID)
	assert.Equal(t, "Keanu Reeves", resp.Results[0].Name)
	assert.Equal(t, "Acting", resp.Results[0].KnownForDepartment)
}

func TestClient_SearchPerson_EmptyQuery(t *testing.T) {
	c := mustNew(t, "http://127.0.0.1:0", "tk")
	defer c.Close()
	_, err := c.SearchPerson(context.Background(), "", "en-US", 1)
	require.Error(t, err)
}

// Empty lang defaults to the client default (mirror of the movie/tv methods).
func TestClient_SearchMethods_EmptyLangDefaults(t *testing.T) {
	var seen struct{ path, lang, page, query string }
	srv := searchSrv(t, collectionListFixture, &seen)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()
	_, err := c.SearchCollection(context.Background(), "matrix", "", 1)
	require.NoError(t, err)
	assert.Equal(t, DefaultLanguage, seen.lang)
}
