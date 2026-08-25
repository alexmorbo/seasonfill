package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const collectionTranslationsFixture = `{
  "id": 726871,
  "name": "Dune Collection",
  "overview": "Epic saga.",
  "poster_path": "/coll_p.jpg",
  "parts": [{"id": 438631, "title": "Dune"}],
  "translations": {
    "translations": [
      {"iso_639_1": "en", "iso_3166_1": "US",
       "data": {"title": "Dune Collection", "overview": "Epic saga."}},
      {"iso_639_1": "ru", "iso_3166_1": "RU",
       "data": {"title": "Дюна: Коллекция", "overview": "Эпическая сага."}}
    ]
  }
}`

func TestClient_GetCollection_RequestsTranslations_Decodes(t *testing.T) {
	var seenAppend string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAppend = r.URL.Query().Get("append_to_response")
		_, _ = w.Write([]byte(collectionTranslationsFixture))
	}))
	t.Cleanup(srv.Close)

	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.GetCollection(context.Background(), 726871, "en-US")
	require.NoError(t, err)
	assert.Equal(t, "translations", seenAppend, "append_to_response=translations required")
	require.NotNil(t, resp.Translations)
	require.Len(t, resp.Translations.Translations, 2)

	byLang := map[string]MovieTranslationData{}
	for _, tr := range resp.Translations.Translations {
		byLang[tr.ISO6391] = tr.Data
	}
	assert.Equal(t, "Дюна: Коллекция", byLang["ru"].Title, "ru localized NAME is data.title")
	assert.Equal(t, "Эпическая сага.", byLang["ru"].Overview)
}

// A collection with no translations sub-resource decodes to a nil pointer —
// no panic downstream (nilable-array contract, mirror of GetSeason).
func TestClient_GetCollection_NoTranslations_NilPointer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":1,"name":"X","parts":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.GetCollection(context.Background(), 1, "en-US")
	require.NoError(t, err)
	assert.Nil(t, resp.Translations)
}
