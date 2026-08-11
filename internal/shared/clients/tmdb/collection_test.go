package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
)

func jsonUnmarshalString(s string, v any) error { return json.Unmarshal([]byte(s), v) }

const collectionFixture = `{
  "id": 726871,
  "name": "Dune Collection",
  "overview": "Epic saga.",
  "poster_path": "/coll_p.jpg",
  "backdrop_path": "/coll_b.jpg",
  "parts": [
    {"id": 438631, "title": "Dune", "original_title": "Dune",
     "original_language": "en", "release_date": "2021-10-22",
     "poster_path": "/d1.jpg", "backdrop_path": "/d1b.jpg",
     "popularity": 120.5, "vote_average": 7.8, "vote_count": 11000},
    {"id": 693134, "title": "Dune: Part Two", "original_title": "Dune: Part Two",
     "original_language": "en", "release_date": "2024-02-27",
     "poster_path": "/d2.jpg", "vote_average": 8.2, "vote_count": 5000},
    {"id": 0, "title": "garbage-no-id"}
  ]
}`

func collectionSrv(t *testing.T, seen *struct{ path, lang string }) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.path = r.URL.Path
		seen.lang = r.URL.Query().Get("language")
		_, _ = w.Write([]byte(collectionFixture))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_GetCollection_HappyPath(t *testing.T) {
	var seen struct{ path, lang string }
	srv := collectionSrv(t, &seen)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.GetCollection(context.Background(), 726871, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "/collection/726871", seen.path)
	assert.Equal(t, "ru-RU", seen.lang)
	require.NotNil(t, resp)
	assert.Equal(t, int64(726871), resp.ID)
	assert.Equal(t, "Dune Collection", resp.Name)
	require.Len(t, resp.Parts, 3)
	assert.Equal(t, int64(438631), resp.Parts[0].ID)
}

func TestClient_GetCollection_DefaultLanguage(t *testing.T) {
	var seen struct{ path, lang string }
	srv := collectionSrv(t, &seen)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	_, err := c.GetCollection(context.Background(), 726871, "")
	require.NoError(t, err)
	assert.Equal(t, DefaultLanguage, seen.lang, "empty per-call lang defers to client default")
}

func TestMapCollectionToCanon(t *testing.T) {
	resp := &CollectionResponse{
		ID: 726871, Name: "Dune Collection", Overview: "Epic saga.",
		PosterPath: "/coll_p.jpg", BackdropPath: "/coll_b.jpg",
	}
	got := MapCollectionToCanon(resp)
	assert.Equal(t, 726871, got.TMDBCollectionID)
	assert.Equal(t, "Dune Collection", got.Name)
	require.NotNil(t, got.Overview)
	assert.Equal(t, "Epic saga.", *got.Overview)
	require.NotNil(t, got.PosterAsset)
	assert.Equal(t, "/coll_p.jpg", *got.PosterAsset)
	require.NotNil(t, got.BackdropAsset)
	assert.Equal(t, "/coll_b.jpg", *got.BackdropAsset)
	// operator/Radarr flags never set by the enrichment mapper.
	assert.False(t, got.Monitored)
	assert.False(t, got.RadarrMonitored)
}

func TestMapCollectionToCanon_NilAndEmpty(t *testing.T) {
	assert.Equal(t, movie.CollectionCanon{}, MapCollectionToCanon(nil))
	got := MapCollectionToCanon(&CollectionResponse{ID: 5, Name: "X"})
	assert.Equal(t, 5, got.TMDBCollectionID)
	assert.Nil(t, got.Overview, "empty overview → nil so COALESCE preserves")
	assert.Nil(t, got.PosterAsset)
}

func TestMapCollectionPartsToCanon(t *testing.T) {
	var resp CollectionResponse
	require.NoError(t, jsonUnmarshalString(collectionFixture, &resp))

	parts := MapCollectionPartsToCanon(&resp)
	require.Len(t, parts, 2, "zero-id part dropped")

	// every part links to the collection id and is a stub.
	for _, p := range parts {
		require.NotNil(t, p.CollectionID)
		assert.Equal(t, 726871, *p.CollectionID)
		assert.Equal(t, movie.HydrationStub, p.Hydration)
		require.NotNil(t, p.TMDBID)
	}
	assert.Equal(t, "Dune", parts[0].Title)
	require.NotNil(t, parts[0].Year)
	assert.Equal(t, 2021, *parts[0].Year)
	require.NotNil(t, parts[0].TMDBRating)
	assert.InDelta(t, 7.8, *parts[0].TMDBRating, 1e-9)
	require.NotNil(t, parts[0].PosterAsset)
	assert.Equal(t, "/d1.jpg", *parts[0].PosterAsset)
}

func TestMapCollectionPartsToCanon_NilEmpty(t *testing.T) {
	assert.Nil(t, MapCollectionPartsToCanon(nil))
	assert.Nil(t, MapCollectionPartsToCanon(&CollectionResponse{ID: 1}))
}
