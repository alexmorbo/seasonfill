package tmdb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenreListTV_PassesLanguage_ParsesPayload(t *testing.T) {
	t.Parallel()

	var seenPath, seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"genres":[
			{"id":10759,"name":"Боевик и Приключения"},
			{"id":16,"name":"мультфильм"}
		]}`)
	}))
	t.Cleanup(srv.Close)

	c := mustNew(t, srv.URL, "test-key")
	defer c.Close()

	got, err := c.GenreListTV(context.Background(), "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "/genre/tv/list", seenPath)
	assert.Contains(t, seenQuery, "language=ru-RU")
	require.Len(t, got.Genres, 2)
	assert.Equal(t, 10759, got.Genres[0].ID)
	assert.Equal(t, "Боевик и Приключения", got.Genres[0].Name)
}

func TestGenreListTV_EmptyLanguage_FallsBackToDefault(t *testing.T) {
	t.Parallel()
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"genres":[]}`)
	}))
	t.Cleanup(srv.Close)

	c := mustNew(t, srv.URL, "test-key")
	defer c.Close()

	_, err := c.GenreListTV(context.Background(), "")
	require.NoError(t, err)
	assert.True(t, strings.Contains(seenQuery, "language="+DefaultLanguage),
		"empty language must collapse to default; got %q", seenQuery)
}
