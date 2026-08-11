package radarr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/arrcore"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

func newClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

// TestClient_PromotesArrcoreSystemStatus verifies the embedded *arrcore.Client
// promotes the shared endpoints so *radarr.Client still satisfies
// dataports.RadarrClient and the wire call is unchanged.
func TestClient_PromotesArrcoreSystemStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/system/status", r.URL.Path)
		assert.Equal(t, "secret", r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"5.0.0","instanceName":"http://radarr"}`))
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv)
	st, err := c.SystemStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "5.0.0", st.Version)
	assert.Equal(t, "test", c.Name())
}

func TestClient_LookupMovie(t *testing.T) {
	var gotPath, gotTerm, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTerm = r.URL.Query().Get("term")
		gotKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
			"title":"Fight Club","titleSlug":"fight-club-603","year":1999,
			"tmdbId":603,"imdbId":"tt0137523","overview":"…",
			"remotePoster":"https://image.tmdb.org/t/p/original/p.jpg",
			"images":[{"coverType":"poster","url":"/MediaCover/1/poster.jpg","remoteUrl":"https://image.tmdb.org/t/p/original/p.jpg"}]
		}]`))
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv)
	res, err := c.LookupMovie(context.Background(), "tmdb:603")
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "/api/v3/movie/lookup", gotPath)
	assert.Equal(t, "tmdb:603", gotTerm)
	assert.Equal(t, "secret", gotKey)
	assert.Equal(t, "Fight Club", res[0].Title)
	assert.Equal(t, 603, res[0].TMDBID)
	assert.Equal(t, "tt0137523", res[0].IMDBID)
	assert.Equal(t, "https://image.tmdb.org/t/p/original/p.jpg", res[0].ImageURL)
	require.Len(t, res[0].Images, 1)
	assert.Equal(t, "poster", res[0].Images[0].CoverType)
}

func TestClient_LookupMovie_EmptyIsNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv)
	res, err := c.LookupMovie(context.Background(), "tmdb:0")
	require.NoError(t, err)
	assert.Empty(t, res)
}

func TestClient_AddMovie_DefaultsAndBody(t *testing.T) {
	var (
		mu      sync.Mutex
		gotPath string
		gotMeth string
		gotBody addMovieRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body) //nolint:bodyclose // httptest
		mu.Lock()
		_ = json.Unmarshal(body, &gotBody)
		gotPath = r.URL.Path
		gotMeth = r.Method
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":77}`))
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv)
	res, err := c.AddMovie(context.Background(), ports.AddMoviePayload{
		TMDBID:           603,
		Title:            "Fight Club",
		TitleSlug:        "fight-club-603",
		Year:             1999,
		QualityProfileID: 4,
		RootFolderPath:   "/movies",
		Monitored:        true,
		SearchOnAdd:      true,
		// MinimumAvailability intentionally empty → defaults to "released".
	})
	require.NoError(t, err)
	assert.Equal(t, 77, res.RadarrMovieID)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "/api/v3/movie", gotPath)
	assert.Equal(t, http.MethodPost, gotMeth)
	assert.Equal(t, 603, gotBody.TMDBID)
	assert.Equal(t, 4, gotBody.QualityProfileID)
	assert.Equal(t, "/movies", gotBody.RootFolderPath)
	assert.True(t, gotBody.Monitored)
	assert.Equal(t, "released", gotBody.MinimumAvailability, "empty minAvail defaults to released")
	assert.True(t, gotBody.AddOptions.SearchForMovie, "SearchOnAdd maps to addOptions.searchForMovie")
}

func TestClient_AddMovie_MinimumAvailabilityOverride(t *testing.T) {
	var (
		mu      sync.Mutex
		gotBody addMovieRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body) //nolint:bodyclose // httptest
		mu.Lock()
		_ = json.Unmarshal(body, &gotBody)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9}`))
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv)
	_, err := c.AddMovie(context.Background(), ports.AddMoviePayload{
		TMDBID:              603,
		MinimumAvailability: "announced",
		SearchOnAdd:         false,
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "announced", gotBody.MinimumAvailability)
	assert.False(t, gotBody.AddOptions.SearchForMovie)
}

func TestClient_ListMovies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/movie", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":1,"title":"Dune","titleSlug":"dune","year":2021,"tmdbId":438631,"imdbId":"tt1160419","monitored":true,"hasFile":true,"minimumAvailability":"released","sizeOnDisk":123},
			{"id":2,"title":"Arrival","titleSlug":"arrival","year":2016,"tmdbId":329865,"monitored":false,"hasFile":false,"sizeOnDisk":0,"statistics":{"movieFileCount":1,"sizeOnDisk":999}}
		]`))
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv)
	movies, err := c.ListMovies(context.Background())
	require.NoError(t, err)
	require.Len(t, movies, 2)
	assert.Equal(t, 1, movies[0].RadarrMovieID)
	assert.True(t, movies[0].HasFile)
	assert.Equal(t, int64(123), movies[0].SizeOnDiskBytes)
	// statistics.sizeOnDisk (999) wins over the top-level sizeOnDisk (0).
	assert.Equal(t, int64(999), movies[1].SizeOnDiskBytes)
	assert.False(t, movies[1].HasFile)
}

// TestClient_StatusErrorSaysRadarr proves the radarr client surfaces a
// StatusError whose text begins "radarr " — never "sonarr" — via the arrcore
// WithArrName("radarr") wiring.
func TestClient_StatusErrorSaysRadarr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kaboom"))
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv)
	_, err := c.ListMovies(context.Background())
	require.Error(t, err)
	var se *arrcore.StatusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, "radarr", se.Arr)
	assert.True(t, strings.HasPrefix(se.Error(), "radarr /api/v3/movie returned status=500"),
		"got %q", se.Error())
}
