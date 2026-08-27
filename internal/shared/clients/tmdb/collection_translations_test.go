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
  },
  "images": {
    "posters": [
      {"file_path": "/en_poster.jpg", "iso_639_1": "en", "vote_average": 7.0, "vote_count": 100},
      {"file_path": "/ru_top.jpg", "iso_639_1": "ru", "vote_average": 8.0, "vote_count": 50}
    ]
  }
}`

func TestClient_GetCollection_RequestsTranslations_Decodes(t *testing.T) {
	var seenAppend, seenImageLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAppend = r.URL.Query().Get("append_to_response")
		seenImageLang = r.URL.Query().Get("include_image_language")
		_, _ = w.Write([]byte(collectionTranslationsFixture))
	}))
	t.Cleanup(srv.Close)

	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.GetCollection(context.Background(), 726871, "en-US")
	require.NoError(t, err)
	assert.Equal(t, "translations,images", seenAppend, "append_to_response=translations,images required (F-08 S2 + S4)")
	// F-08 S4: without include_image_language the appended images sub-resource is
	// filtered to the base language (en) only, so the ru poster pick writes NULL.
	// Same en,ru,null UNION the all-langs GetTV/GetSeason paths carry.
	assert.Equal(t, "en,ru,null", seenImageLang,
		"include_image_language MUST carry the en,ru,null union or non-base posters are dropped (F-08 S4)")
	require.NotNil(t, resp.Translations)
	require.Len(t, resp.Translations.Translations, 2)

	byLang := map[string]MovieTranslationData{}
	for _, tr := range resp.Translations.Translations {
		byLang[tr.ISO6391] = tr.Data
	}
	assert.Equal(t, "Дюна: Коллекция", byLang["ru"].Title, "ru localized NAME is data.title")
	assert.Equal(t, "Эпическая сага.", byLang["ru"].Overview)

	// F-08 S4 — images sub-resource decodes into the reused TVImages struct,
	// with posters[*].iso_639_1 populated for the per-language poster pick.
	require.NotNil(t, resp.Images)
	require.Len(t, resp.Images.Posters, 2)
	imgByLang := map[string]TVImage{}
	for _, p := range resp.Images.Posters {
		require.NotNil(t, p.ISO6391)
		imgByLang[*p.ISO6391] = p
	}
	assert.Equal(t, "/ru_top.jpg", imgByLang["ru"].FilePath, "ru poster decoded from images.posters[]")
	assert.Equal(t, 8.0, imgByLang["ru"].VoteAverage)
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
