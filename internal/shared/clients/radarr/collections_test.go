package radarr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

func TestClient_GetCollections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/collection", r.URL.Path)
		assert.Equal(t, "secret", r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":12,"title":"Dune Collection","tmdbId":726871,"monitored":false,
			 "searchOnAdd":true,"qualityProfileId":4,"minimumAvailability":"released",
			 "rootFolderPath":"/movies"}
		]`))
	}))
	t.Cleanup(srv.Close)

	cols, err := newClient(t, srv).GetCollections(context.Background())
	require.NoError(t, err)
	require.Len(t, cols, 1)
	assert.Equal(t, 12, cols[0].ID)
	assert.Equal(t, 726871, cols[0].TMDBID)
	assert.False(t, cols[0].Monitored)
	assert.Equal(t, "released", cols[0].MinimumAvailability)
}

func TestClient_GetCollection_ByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/collection/12", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":12,"title":"Dune Collection","tmdbId":726871,"monitored":true}`))
	}))
	t.Cleanup(srv.Close)

	col, err := newClient(t, srv).GetCollection(context.Background(), 12)
	require.NoError(t, err)
	assert.Equal(t, 12, col.ID)
	assert.True(t, col.Monitored)
}

func TestClient_PutCollection(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody collectionDTO
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":12,"monitored":true}`))
	}))
	t.Cleanup(srv.Close)

	err := newClient(t, srv).PutCollection(context.Background(), ports.RadarrCollection{
		ID: 12, TMDBID: 726871, Monitored: true, QualityProfileID: 4,
		MinimumAvailability: "released", RootFolderPath: "/movies",
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/api/v3/collection/12", gotPath)
	assert.True(t, gotBody.Monitored)
	assert.Equal(t, 726871, gotBody.TMDBID)
}
